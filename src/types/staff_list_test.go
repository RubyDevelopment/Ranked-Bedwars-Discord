package types

import "testing"

// /staff has the staff role among its requirements (so it is chosen on the Commands page) without becoming a staff-only command.
func TestStaffListDoesNotNeedTheStaffRole(t *testing.T) {
	if !HasRequirementKey(StaffListRequirements, ReqRole, StaffRoleKey) {
		t.Fatal("/staff does not declare the staff role")
	}
	if RequiresRole(StaffListRequirements, StaffRoleKey) {
		t.Fatal("/staff would only be usable by staff")
	}
	if !RequiresRole(StaffRequirements, StaffRoleKey) || !RequiresRole(PunishmentRequirements("warn_logs"), StaffRoleKey) {
		t.Fatal("staff commands no longer need the staff role")
	}
}
