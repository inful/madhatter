package cmd

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/alecthomas/kong"
	"github.com/inful/madhatter/internal/api"
	"github.com/inful/madhatter/internal/calendar"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/rota"
)

const (
	// File permissions for exported ICS files.
	filePermissionICS = 0o600
)

var CLI struct {
	Serve struct {
		Port string `default:"8080" arg:""`
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
			Type     string `help:"Leave type (sick/vacation/other)" arg:""`
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

	// Map command to handler
	handlers := map[string]func(*database.DB){
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
	}

	if handler, exists := handlers[command]; exists {
		handler(db)
	} else {
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		os.Exit(1)
	}
}

func serveCommand(db *database.DB) {
	server := api.NewServer(db)
	log.Printf("Starting server on port %s\n", CLI.Serve.Port)
	if err := server.Start(CLI.Serve.Port); err != nil {
		log.Printf("Server error: %v\n", err)
		os.Exit(1)
	}
}

func teamAddCommand(db *database.DB) {
	id, err := db.AddTeamMember(CLI.Team.Add.Name, CLI.Team.Add.Email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	log.Printf("Added team member: %s (%s) - ID: %s\n", CLI.Team.Add.Name, CLI.Team.Add.Email, id)
}

func teamListCommand(db *database.DB) {
	members, err := db.GetActiveTeamMembers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	log.Println("Active Team Members:")
	for i, m := range members {
		log.Printf("%d. %s - %s (ID: %s)\n", i+1, m.Name, m.Email, m.ID)
	}
}

func leaveReportCommand(db *database.DB) {
	// Get member by email or ID
	member, err := db.GetMemberByEmail(CLI.Leave.Report.MemberID)
	if err != nil {
		// Try as UUID
		member = &database.TeamMember{ID: CLI.Leave.Report.MemberID}
	}

	leaveID, err := db.CreateLeaveRecord(member.ID, CLI.Leave.Report.Type, CLI.Leave.Report.Start, CLI.Leave.Report.End)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Assign covers
	engine := rota.NewEngine(db)
	err = engine.AssignCoversForLeave(leaveID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Covers not assigned: %v\n", err)
	}

	log.Printf("Leave reported: %s from %s to %s (ID: %s)\n",
		CLI.Leave.Report.MemberID, CLI.Leave.Report.Start, CLI.Leave.Report.End, leaveID)
}

func leaveListCommand(db *database.DB) {
	leaves, err := db.GetLeaveRecords()
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
			log.Printf("%d. %s - %s to %s (%s) [%s]\n", i+1, l.MemberID, l.StartDate, l.EndDate, l.Type, l.Status)
		}
	}
}

func scheduleGenerateCommand(db *database.DB) {
	startDate, _ := time.Parse("2006-01-02", CLI.Schedule.Generate.Start)
	endDate, _ := time.Parse("2006-01-02", CLI.Schedule.Generate.End)

	engine := rota.NewEngine(db)
	err := engine.GenerateSchedule(startDate, endDate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	log.Printf("Schedule generated from %s to %s\n", CLI.Schedule.Generate.Start, CLI.Schedule.Generate.End)
}

func scheduleViewCommand(db *database.DB) {
	// Get schedule for specific date
	dateStr := CLI.Schedule.View.Date
	assignments, err := db.GetAssignmentsByDate(dateStr)
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

func calendarSubscribeCommand(db *database.DB) {
	// Get member by email
	member, err := db.GetMemberByEmail(CLI.Calendar.Subscribe.Email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	token, err := db.CreateCalendarSubscription(member.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	log.Printf("Calendar subscription created for %s\n", CLI.Calendar.Subscribe.Email)
	log.Printf("Calendar URL: http://localhost:8080/calendar/%s/ics\n", token)
	log.Printf("Subscribe in your calendar app using this URL\n")
}

func calendarExportCommand(db *database.DB) {
	// Get member by email
	member, err := db.GetMemberByEmail(CLI.Calendar.Export.Email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Get subscription token (needed for ICS generation)
	_, err = db.CreateCalendarSubscription(member.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Generate ICS content
	const defaultLookaheadDays = 90
	assignments, err := db.GetUpcomingAssignments(member.ID, defaultLookaheadDays)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting assignments: %v\n", err)
		os.Exit(1)
	}

	// Generate ICS content using calendar package
	icsContent := calendar.GenerateICS(assignments, member.Name)

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
