package types

// Ticket statuses.
const (
	TicketStatusOpen    = "open"
	TicketStatusClaimed = "claimed"
	TicketStatusClosed  = "closed"
)

// Ticket modes.
const (
	TicketModeThread  = "thread"
	TicketModeChannel = "channel"
)

// TicketPanelConfig is the ticket-specific half of a ticket panel.
type TicketPanelConfig struct {
	Categories []*TicketCategory `json:"categories"`
	Settings   TicketSettings    `json:"settings"`
}

// TicketCategory is one button on a ticket panel.
type TicketCategory struct {
	ID                string        `json:"id"`
	Label             string        `json:"label"`
	ButtonLabel       string        `json:"button_label"`
	Emoji             string        `json:"emoji"`
	Description       string        `json:"description"`
	ButtonStyle       int           `json:"button_style"`
	NameFormat        string        `json:"name_format"`
	ChannelNameFormat string        `json:"channel_name_format,omitempty"`
	ThreadNameFormat  string        `json:"thread_name_format,omitempty"`
	StaffRoleIDs      []string      `json:"staff_role_ids"`
	PingStaff         bool          `json:"ping_staff"`
	Welcome           TicketWelcome `json:"welcome"`
	CategoryID        string        `json:"category_id"` // Parent category ID for channel-based tickets
}

// TicketWelcome is the message posted inside a freshly opened ticket. Title,
// Body and Footer accept the placeholders {user}, {username}, {category},
// {reference}, {number}, {staff} and {server}.
type TicketWelcome struct {
	Title                 string                `json:"title"`
	Body                  string                `json:"body"`
	Footer                string                `json:"footer"`
	Color                 int                   `json:"color"`
	NoColor               bool                  `json:"no_color,omitempty"` // no side colour: the bar takes the embed's own background colour
	ThumbnailURL          string                `json:"thumbnail_url"`
	HideDetails           bool                  `json:"hide_details"`
	AllowedMentions       *PanelAllowedMentions `json:"allowed_mentions,omitempty"`
	SuppressEmbeds        bool                  `json:"suppress_embeds,omitempty"`
	SuppressNotifications bool                  `json:"suppress_notifications,omitempty"`
	// Fields are the embed fields under the welcome text (name, value and whether they sit side by side). Name and value
	// take the same placeholders as the text, plus {created} and {status}. Without Fields (nil) the ticket details are
	// shown as the standard five (Reference, Category, Owner, Created At, Status) unless HideDetails is on. An empty list
	// means no fields. A pointer, so that an empty list is kept when the bot saves the panel again.
	Fields *[]TicketField `json:"fields,omitempty"`
}

// TicketField is one embed field of the ticket welcome message.
type TicketField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// TicketSettings holds the behaviour shared by every category of a panel.
type TicketSettings struct {
	ThreadChannelID string   `json:"thread_channel_id"`
	LogChannelID    string   `json:"log_channel_id"`
	StaffRoleIDs    []string `json:"staff_role_ids"`
	MaxOpenPerUser  int      `json:"max_open_per_user"`
	ClaimEnabled    bool     `json:"claim_enabled"`
	DMTranscript    bool     `json:"dm_transcript"`
	DeleteOnClose   bool     `json:"delete_on_close"`
	OwnerCanClose   bool     `json:"owner_can_close"`
	Mode            string   `json:"mode"` // "thread" or "channel"
	// InactivityEnabled turns the idle reminder on. Tickets are never closed for inactivity.
	InactivityEnabled       bool   `json:"inactivity_enabled"`
	InactivityReminderHours int    `json:"inactivity_reminder_hours"`
	InactivityMessage       string `json:"inactivity_message"`
	// InactivityMessageID is a saved Embed Builder message (message, embeds or Components V2) used for the reminder
	// instead of InactivityMessage; empty or missing falls back to the plain text.
	InactivityMessageID   string `json:"inactivity_message_id"`
	InlineCategoryButtons bool   `json:"inline_category_buttons"`
}

// TicketState is the runtime state of every ticket panel in a guild.
type TicketState struct {
	Counter   int                          `json:"counter"`
	Open      map[string]*Ticket           `json:"open"`
	Stats     map[string]*TicketStaffStats `json:"stats"`
	History   []*Ticket                    `json:"history"`
	Blacklist []*TicketBlacklistEntry      `json:"blacklist,omitempty"`
}

// TicketBlacklistEntry prevents a member from opening new support tickets.
// ExpiresAt is a Unix timestamp; zero means the entry is permanent.
type TicketBlacklistEntry struct {
	UserID    string `json:"user_id"`
	Reason    string `json:"reason"`
	AddedBy   string `json:"added_by"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

// Ticket is one support ticket, keyed in TicketState.Open by its thread ID.
type Ticket struct {
	Ref                    string           `json:"ref"`
	Number                 int              `json:"number"`
	ThreadID               string           `json:"thread_id"`
	ThreadName             string           `json:"thread_name"`
	PanelID                string           `json:"panel_id"`
	CategoryID             string           `json:"category_id"`
	CategoryLabel          string           `json:"category_label"`
	OwnerID                string           `json:"owner_id"`
	OwnerName              string           `json:"owner_name"`
	Status                 string           `json:"status"`
	ClaimedBy              string           `json:"claimed_by"`
	ClaimedAt              int64            `json:"claimed_at"`
	CreatedAt              int64            `json:"created_at"`
	ClosedAt               int64            `json:"closed_at"`
	ClosedBy               string           `json:"closed_by"`
	CloseReason            string           `json:"close_reason"`
	WelcomeMessageID       string           `json:"welcome_message_id"`
	Mode                   string           `json:"mode"` // "thread" or "channel"
	LastActivityAt         int64            `json:"last_activity_at,omitempty"`
	LastInactivityNoticeAt int64            `json:"last_inactivity_notice_at,omitempty"`
	ArchiveURL             string           `json:"archive_url,omitempty"`
	Logs                   []TicketLogEntry `json:"logs,omitempty"`
}

type TicketLogEntry struct {
	Type      string `json:"type"`
	ActorID   string `json:"actor_id,omitempty"`
	Message   string `json:"message"`
	CreatedAt int64  `json:"created_at"`
}

// TicketStaffStats feeds the ticket leaderboard.
type TicketStaffStats struct {
	Claimed       int   `json:"claimed"`
	Closed        int   `json:"closed"`
	LastClaimedAt int64 `json:"last_claimed_at"`
}

type TicketContext struct {
	Ticket   *Ticket
	Panel    *InteractivePanel
	Category *TicketCategory
}

// TicketLeaderboardRow is one ranked staff member.
type TicketLeaderboardRow struct {
	StaffID string
	Stats   TicketStaffStats
}
