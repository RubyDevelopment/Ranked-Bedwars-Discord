package types

// DashboardStaff is one entry of the guild state's "staff" list: someone who
// reaches the dashboard through Discord permissions alone.
type DashboardStaff struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Via       string `json:"via"` // "Owner", "Administrator" or "Manage Server"
}
