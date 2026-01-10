# Plan to Remove Leave Type Functionality

## Current State Analysis

**Database Layer (Correct):**
- ✅ `internal/database/sqlc/schema.sql`: No `type` field in `leave_records` table
- ✅ `internal/database/models.go`: No `Type` field in `LeaveRecord` struct
- ✅ `internal/database/leave.go`: `CreateLeaveRecord(ctx, memberID, startDate, endDate)` - no type parameter
- ✅ `internal/database/sqlc/queries/leave_records.sql`: No type references

**Web Layer (Needs Fixing):**
- ❌ `internal/web/handlers.go`:
  - Line 566: `leaveType := r.FormValue("type")` - extracts type from form
  - Line 570: `CreateLeaveRecord(ctx, memberID, leaveType, startDate, endDate)` - passes wrong number of args
  - Line 49-52: `presenceLeave` struct has `Type string` field
  - Line 414: `away = append(away, presenceLeave{Member: member, Type: leave.Type})` - uses Type field

**Templates (Needs Fixing):**
- ❌ `internal/web/templates/leave_report.html`: Has type selection dropdown
- ❌ `internal/web/templates/dashboard.html`: May display leave types

**Tests (Needs Fixing):**
- ❌ `internal/database/leave_test.go`: Expects Type field in assertions

## Changes Required

### 1. Update `internal/web/handlers.go`
- Remove `leaveType := r.FormValue("type")` from `handleLeaveReport`
- Fix `CreateLeaveRecord` call to use correct signature
- Remove `Type` field from `presenceLeave` struct
- Remove `Type: leave.Type` from `presenceLeave` initialization

### 2. Update `internal/web/templates/leave_report.html`
- Remove type selection dropdown

### 3. Update `internal/web/templates/dashboard.html`
- Remove any leave type display

### 4. Update `internal/database/leave_test.go`
- Remove Type field expectations

## Implementation Order
1. Fix web handlers (most critical)
2. Fix templates
3. Fix tests
4. Test the changes