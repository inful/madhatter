package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/alecthomas/kong"
	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/api"
	"github.com/inful/madhatter/internal/calendar"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/holiday"
	"github.com/inful/madhatter/internal/rota"
	"github.com/inful/madhatter/internal/wfh"
)

const (
	// File permissions for exported ICS files.
	filePermissionICS = 0o600
)

var CLI struct {
	Serve struct {
		Port           string `default:"8080" arg:""`
		Development    bool   `default:"false" help:"Enable development mode using fake OAuth to bypass full OAuth setup for local development"`
		ReassignCovers bool   `default:"true" help:"On startup, re-run the cover-assignment algorithm against every leave (idempotent on stable data — safe to leave enabled)"`
	} `cmd:"" help:"Start web server"`

	Team struct {
		Add struct {
			Name  string `help:"Team member name" arg:""`
			Email string `help:"Team member email" arg:""`
		} `cmd:"" help:"Add team member"`

		List struct{} `cmd:"" help:"List team members"`
	} `cmd:"" help:"Team management"`

	Leave struct {
		Report struct {
			MemberID string `help:"Member ID or email" arg:""`
			Start    string `help:"Start date (YYYY-MM-DD)" arg:""`
			End      string `help:"End date (YYYY-MM-DD)" arg:""`
		} `cmd:"" help:"Report leave"`

		List struct{} `cmd:"" help:"List leave records"`
	} `cmd:"" help:"Leave management"`

	Schedule struct {
		Generate struct {
			Start string `help:"Start date (YYYY-MM-DD)" arg:""`
			End   string `help:"End date (YYYY-MM-DD)" arg:""`
		} `cmd:"" help:"Generate schedule"`

		View struct {
			Date string `help:"Date to view (YYYY-MM-DD)" arg:""`
		} `cmd:"" help:"View schedule for date"`
	} `cmd:"" help:"Schedule management"`

	Calendar struct {
		Subscribe struct {
			Email string `help:"Member email" arg:""`
		} `cmd:"" help:"Create calendar subscription"`

		Export struct {
			Email  string `help:"Member email" arg:""`
			Output string `help:"Output file path" arg:""`
		} `cmd:"" help:"Export ICS file"`
	} `cmd:"" help:"Calendar management"`

	ReassignCovers struct{} `cmd:"" help:"Re-run the cover-assignment algorithm against all leaves (idempotent on stable data — safe to run at any time)"`

	WFH struct {
		Purge struct {
			Apply  bool   `help:"Actually delete. Without this, prints a dry-run summary."`
			Before string `help:"Override the cutoff date (YYYY-MM-DD). Default: start of the previous quota period."`
		} `cmd:"" help:"Purge WFH requests older than the previous quota period"`
		Report struct {
			MemberID string `name:"member-id" help:"Member ID (UUID) or email" arg:""`
		} `cmd:"" help:"Report WFH for today (settled inline against the on-site floor)"`
	} `cmd:"" help:"WFH management"`
}

func Execute() {
	// Initialize database
	db, err := database.New("support_rota.db")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ctx := kong.Parse(&CLI)
	command := ctx.Command()
	ctxBg := context.Background()

	// Map command to handler
	handlers := map[string]func(context.Context, *database.DB){
		"serve":                   serveCommand,
		"serve <port>":            serveCommand,
		"team add <name> <email>": teamAddCommand,
		"team list":               teamListCommand,
		"leave report <member-id> <type> <start> <end>": leaveReportCommand,
		"leave list":                       leaveListCommand,
		"schedule generate <start> <end>":  scheduleGenerateCommand,
		"schedule view <date>":             scheduleViewCommand,
		"calendar subscribe <email>":       calendarSubscribeCommand,
		"calendar export <email> <output>": calendarExportCommand,
		"reassign-covers":                  reassignCoversCommand,
		"wfh purge":                        wfhPurgeCommand,
		"wfh report <member-id>":           wfhReportTodayCommand,
	}

	if handler, exists := handlers[command]; exists {
		handler(ctxBg, db)
	} else {
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		os.Exit(1)
	}
}

func serveCommand(ctx context.Context, db *database.DB) {
	// Run the cover reassignment before bringing the server up. The
	// operation is idempotent on a steady-state rota, so this is a
	// no-op when nothing has changed; when the algorithm HAS changed
	// (e.g. a bug-fix deploy), the existing on-disk covers converge
	// to the new algorithm's output before the first request is
	// served. Disable with --reassign-covers=false for debugging.
	if CLI.Serve.ReassignCovers {
		runCoverReassignment(ctx, db)
	}

	server, err := api.NewServer(db, CLI.Serve.Development) //nolint:contextcheck // ctx is reserved for the long-running server lifecycle; the constructor uses its own short-lived contexts
	if err != nil {
		log.Fatalf("Failed to create server: %v\n", err)
	}
	log.Printf("Starting server on port %s\n", CLI.Serve.Port)
	if CLI.Serve.Development {
		log.Println("! DEVELOPMENT MODE ENABLED - Using fake OAuth authentication")
	}
	if err := server.Start(ctx, CLI.Serve.Port); err != nil {
		log.Printf("Server error: %v\n", err)
		os.Exit(1)
	}
}

func teamAddCommand(ctx context.Context, db *database.DB) {
	id, err := db.AddTeamMember(ctx, CLI.Team.Add.Name, CLI.Team.Add.Email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	log.Printf("Added team member: %s (%s) - ID: %s\n", CLI.Team.Add.Name, CLI.Team.Add.Email, id)
}

func teamListCommand(ctx context.Context, db *database.DB) {
	members, err := db.GetActiveTeamMembers(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	log.Println("Active Team Members:")
	for i, m := range members {
		log.Printf("%d. %s - %s (ID: %s)\n", i+1, m.Name, m.Email, m.ID)
	}
}

func leaveReportCommand(ctx context.Context, db *database.DB) {
	// Get member by email or ID
	member, err := db.GetMemberByEmail(ctx, CLI.Leave.Report.MemberID)
	if err != nil {
		// Try as UUID
		member = &database.TeamMember{ID: CLI.Leave.Report.MemberID}
	}

	leaveID, err := db.CreateLeaveRecord(ctx, member.ID, CLI.Leave.Report.Start, CLI.Leave.Report.End, database.LeaveTypeLeave)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Assign covers
	engine := rota.NewEngine(db)
	err = engine.AssignCoversForLeave(ctx, leaveID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Covers not assigned: %v\n", err)
	}

	log.Printf("Leave reported: %s from %s to %s (ID: %s)\n",
		CLI.Leave.Report.MemberID, CLI.Leave.Report.Start, CLI.Leave.Report.End, leaveID)
}

func leaveListCommand(ctx context.Context, db *database.DB) {
	leaves, err := db.GetLeaveRecords(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	log.Println("Leave Records:")
	if len(leaves) == 0 {
		log.Println("No leave records found")
	} else {
		for i := range leaves {
			l := &leaves[i]
			log.Printf("%d. %s - %s to %s [%s]\n", i+1, l.MemberID, l.StartDate, l.EndDate, l.Status)
		}
	}
}

func scheduleGenerateCommand(ctx context.Context, db *database.DB) {
	startDate, _ := time.Parse("2006-01-02", CLI.Schedule.Generate.Start)
	endDate, _ := time.Parse("2006-01-02", CLI.Schedule.Generate.End)

	engine := rota.NewEngine(db)
	err := engine.GenerateSchedule(ctx, startDate, endDate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	log.Printf("Schedule generated from %s to %s\n", CLI.Schedule.Generate.Start, CLI.Schedule.Generate.End)
}

func scheduleViewCommand(ctx context.Context, db *database.DB) {
	// Get schedule for specific date
	dateStr := CLI.Schedule.View.Date
	assignments, err := db.GetAssignmentsByDate(ctx, dateStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	log.Printf("Schedule for %s:\n", dateStr)
	if len(assignments) == 0 {
		log.Println("No assignments found")
	} else {
		for i, a := range assignments {
			status := "Normal"
			if a.IsCover {
				status = "COVER"
			}
			log.Printf("%d. %s - %s (%s)\n", i+1, a.MemberName, a.MemberEmail, status)
		}
	}
}

func calendarSubscribeCommand(ctx context.Context, db *database.DB) {
	// Get member by email
	member, err := db.GetMemberByEmail(ctx, CLI.Calendar.Subscribe.Email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	token, err := db.CreateCalendarSubscription(ctx, member.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	log.Printf("Calendar subscription created for %s\n", CLI.Calendar.Subscribe.Email)
	log.Printf("Calendar URL: http://localhost:8080/calendar/%s/ics\n", token)
	log.Printf("Subscribe in your calendar app using this URL\n")
}

func calendarExportCommand(ctx context.Context, db *database.DB) {
	// Get member by email
	member, err := db.GetMemberByEmail(ctx, CLI.Calendar.Export.Email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Get subscription token (needed for ICS generation)
	_, err = db.CreateCalendarSubscription(ctx, member.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Generate ICS content
	const defaultLookaheadDays = 90
	assignments, err := db.GetUpcomingAssignments(ctx, member.ID, defaultLookaheadDays)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting assignments: %v\n", err)
		os.Exit(1)
	}

	// Generate ICS content using new calendar library
	icsContent, err := calendar.GenerateICalFromAssignments(assignments, member.Name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating calendar: %v\n", err)
		os.Exit(1)
	}

	// Write to file
	filePath := CLI.Calendar.Export.Output
	if len(filePath) < 4 || filePath[len(filePath)-4:] != ".ics" {
		filePath += ".ics"
	}

	err = os.WriteFile(filePath, []byte(icsContent), filePermissionICS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing ICS file: %v\n", err)
		os.Exit(1)
	}

	log.Printf("ICS file exported for %s to %s\n", member.Name, filePath)
	log.Printf("Total assignments: %d\n", len(assignments))
}

// reassignCoversCommand re-runs the cover-assignment algorithm against
// every leave in the database. The operation is idempotent on a
// steady-state rota, so it's safe to invoke at any time — including
// to recover from manual cover edits or to confirm a deploy.
func reassignCoversCommand(ctx context.Context, db *database.DB) {
	//nolint:contextcheck // holiday service goroutines are fire-and-forget; the call chain through InitializeHolidayService does not accept a context.
	maintenance, stop := buildReassignmentMaintenance(db)

	result, err := maintenance.ReassignCovers(ctx)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Reassignment failed: %v\n", err)
		os.Exit(1)
	}
	log.Printf("Reassignment: %d leaves processed, %d covers changed\n",
		result.LeavesProcessed, result.CoversChanged)
}

// runCoverReassignment is the startup-hook entry point used by
// `serve`. It runs the reassignment unconditionally on every startup:
// because the algorithm is idempotent, a steady-state rota is a
// cheap no-op, and a freshly-deployed binary with a new algorithm
// will self-heal into the new output before the server accepts its
// first request. An error is logged and startup continues.
func runCoverReassignment(ctx context.Context, db *database.DB) {
	//nolint:contextcheck // holiday service goroutines are fire-and-forget; the call chain through InitializeHolidayService does not accept a context.
	maintenance, stop := buildReassignmentMaintenance(db)
	defer stop()

	result, err := maintenance.ReassignCovers(ctx)
	if err != nil {
		log.Printf("Cover reassignment error (continuing with on-disk data): %v\n", err)
		return
	}
	if len(result.Failures) > 0 {
		log.Printf("Cover reassignment: %d leave(s) failed to process and were skipped: %v\n",
			len(result.Failures), result.Failures)
	}
	if result.CoversChanged == 0 {
		log.Printf("Cover reassignment: %d leaves processed, no changes\n", result.LeavesProcessed)
		return
	}
	log.Printf("Cover reassignment: %d leaves processed, %d covers changed\n",
		result.LeavesProcessed, result.CoversChanged)
}

// buildReassignmentMaintenance constructs a *rota.ScheduleMaintenance
// configured to match what api.NewServer wires up for the live server:
// holiday service initialized from the database, holiday checker
// attached on the engine. The reassignment runner must use the same
// configuration as production or it would diverge on any leave that
// spans a holiday — a leave that the live server would skip would
// get a cover from the cmd-side runner.
//
// Note: the returned maintenance is intentionally NOT wired with a
// notifier. The reassignment follows the same HandleLeaveChange
// convention as the server's newScheduleMaintenance — bulk cover
// processing is silent. Only the single-leave AssignCoversForLeave
// path (taken by the API endpoint on a fresh leave report) fires
// CoverAssigned events. The cmd runner is for catch-up / self-heal
// runs that shouldn't spam the cover assignee with notifications
// every time a deploy reconverges the rota.
//
// The returned stop function releases the holiday service background
// goroutine (if any). Callers must invoke it before the process exits.
func buildReassignmentMaintenance(db *database.DB) (*rota.ScheduleMaintenance, func()) {
	maintenance := rota.NewScheduleMaintenance(db)
	holidayService, err := holiday.InitializeHolidayService(db)
	if err != nil {
		log.Printf("Cover reassignment: holiday service init failed (continuing without): %v\n", err)
		return maintenance, func() {}
	}
	if holidayService != nil {
		maintenance.SetHolidayChecker(holidayService.ShouldSkipDate)
	}
	return maintenance, func() {
		if holidayService != nil {
			// Service.Stop() returns "scheduler is not running" when
			// HOLIDAY_URLS was empty (no scheduler was started). That's
			// a normal no-op case, not a failure to log about.
			if stopErr := holidayService.Stop(); stopErr != nil && !isNoOpHolidayStopErr(stopErr) {
				log.Printf("Cover reassignment: holiday service stop failed: %v\n", stopErr)
			}
		}
	}
}

// isNoOpHolidayStopErr reports whether a holiday.Service.Stop error
// is just the well-known "scheduler was never started" response —
// which happens on every boot when HOLIDAY_URLS was empty. The
// service returns this even though stopping was a no-op.
func isNoOpHolidayStopErr(err error) bool {
	return err != nil && err.Error() == "scheduler is not running"
}

// wfhPurgeCommand purges wfh_requests rows older than the start of the
// previous quota period. Dry-run by default; pass --apply to commit.
// --before YYYY-MM-DD overrides the period-derived cutoff for one-off
// catch-up cleans.
func wfhPurgeCommand(ctx context.Context, db *database.DB) {
	svc := wfh.NewService(db, wfh.LoadConfigFromEnv())
	if !svc.IsPurgeEnabled() {
		fmt.Fprintln(os.Stderr, "WFH past-period purge is disabled (WFH_ENABLED=false). Nothing to do.")
		os.Exit(1)
	}

	override := CLI.WFH.Purge.Before
	if override != "" {
		// Validate the override before counting so an obviously bad
		// --before surfaces as an input error, not a silent zero.
		if _, err := time.Parse("2006-01-02", override); err != nil {
			fmt.Fprintf(os.Stderr, "Invalid --before date (expected YYYY-MM-DD): %s\n", override)
			os.Exit(1)
		}
	}

	cutoff, affected, err := runWFHPurge(ctx, db, svc, override, CLI.WFH.Purge.Apply)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WFH purge failed: %v\n", err)
		os.Exit(1)
	}

	if CLI.WFH.Purge.Apply {
		log.Printf("WFH past-period purge: deleted %d rows with date < %s\n", affected, cutoff)
		return
	}
	log.Printf("WFH past-period purge (dry-run): %d rows would be deleted with date < %s\n", affected, cutoff)
	log.Printf("Re-run with --apply to commit.\n")
}

// runWFHPurge routes the (override, apply) matrix to the right DB or
// service method and normalises the (int64, error) and (string, int64,
// error) return shapes. Pulled out of wfhPurgeCommand to keep the
// apply/dry-run branches at a single nesting level.
func runWFHPurge(ctx context.Context, db *database.DB, svc *wfh.Service, override string, apply bool) (string, int64, error) {
	if apply {
		if override != "" {
			n, err := db.PurgeWFHRequestsBefore(ctx, override)
			return override, n, err
		}
		return svc.PurgePastPeriods(ctx)
	}
	if override != "" {
		n, err := db.CountWFHRequestsBefore(ctx, override)
		return override, n, err
	}
	return svc.PurgePastPeriodsDryRun(ctx)
}

// wfhReportTodayCommand is the CLI mirror of the dashboard "WFH
// today" button. Resolves the member by ID or email, calls
// Service.ReportToday (which creates + settles inline), and prints
// the outcome. Mirrors the web/API behavior so a script running
// while the operator is away from the browser produces the same
// approved-or-denied answer.
func wfhReportTodayCommand(ctx context.Context, db *database.DB) {
	svc := wfh.NewService(db, wfh.LoadConfigFromEnv())
	if !svc.IsEnabled() {
		fmt.Fprintln(os.Stderr, "WFH feature is disabled (WFH_ENABLED=false). Nothing to do.")
		os.Exit(1)
	}

	memberID := resolveWFHReportMemberID(ctx, db, CLI.WFH.Report.MemberID)

	req, err := svc.ReportToday(ctx, memberID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WFH report-today failed: %v\n", err)
		os.Exit(1)
	}

	log.Printf("WFH for %s on %s: %s\n", req.MemberID, req.Date, req.Status)
}

// resolveWFHReportMemberID accepts a CLI member identifier that may
// be a UUID or an email. The web/API flow derives the member from
// the session; the CLI doesn't have a session, so the operator
// passes the ID explicitly. Tries UUID first (no IO), then falls
// back to the email lookup. Errors out with a clear message rather
// than letting the service-side validation do the talking.
func resolveWFHReportMemberID(ctx context.Context, db *database.DB, idOrEmail string) string {
	if _, err := uuid.Parse(idOrEmail); err == nil {
		return idOrEmail
	}
	m, err := db.GetMemberByEmail(ctx, idOrEmail)
	if err != nil || m == nil {
		fmt.Fprintf(os.Stderr, "Could not resolve member %q (not a UUID and no team member found)\n", idOrEmail)
		os.Exit(1)
	}
	return m.ID
}
