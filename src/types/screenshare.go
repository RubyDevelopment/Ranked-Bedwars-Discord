package types

// Screenshare request kinds and statuses. A "ticket" is the private channel a screensharer works in; a "review" is the
// investigation request for a player with the SS Restricted role, which a screensharer must approve before a ticket opens.
const (
	ScreenshareKindTicket = "ticket"
	ScreenshareKindReview = "review"

	ScreenshareActive   = "active"   // ticket: waiting for a screensharer to claim it
	ScreenshareClaimed  = "claimed"  // ticket: a screensharer is on it
	ScreenshareClosed   = "closed"   // ticket: finished
	ScreenshareExpired  = "expired"  // ticket: nobody claimed it in time
	ScreensharePending  = "pending"  // review: waiting for a decision
	ScreenshareApproved = "approved" // review: a ticket was opened
	ScreenshareDenied   = "denied"   // review: refused
)

// ScreenshareRequest is one screenshare ticket or SS Restricted review. Every change is stored as a new snapshot of it in
// the guild's event log, and the newest snapshot of an ID is the request as it stands.
type ScreenshareRequest struct {
	ID                  int    `json:"id"`
	Kind                string `json:"kind"`
	Status              string `json:"status"`
	TargetID            string `json:"target_id"`
	TargetName          string `json:"target_name,omitempty"` // their registered Minecraft username
	RequesterID         string `json:"requester_id"`
	Reason              string `json:"reason"`
	EvidenceURL         string `json:"evidence_url,omitempty"`
	EvidenceImage       bool   `json:"evidence_image,omitempty"` // shown in the ticket itself instead of linked
	Timestamps          string `json:"timestamps,omitempty"`
	ChannelID           string `json:"channel_id,omitempty"` // the ticket channel, or the review's channel
	MessageID           string `json:"message_id,omitempty"`
	ClaimedBy           string `json:"claimed_by,omitempty"`
	ReviewedBy          string `json:"reviewed_by,omitempty"`
	ApprovedBy          string `json:"approved_by,omitempty"`
	TicketChannelID     string `json:"ticket_channel_id,omitempty"` // review: the ticket its approval opened
	CreatedAt           int64  `json:"created_at"`
	ExpiresAt           int64  `json:"expires_at,omitempty"`
	CloseAt             int64  `json:"close_at,omitempty"` // a screensharer scheduled the ticket to close then; 0: not scheduled
	ClosedAt            int64  `json:"closed_at,omitempty"`
	ClosedBy            string `json:"closed_by,omitempty"`
	RestrictionRoleID   string `json:"restriction_role_id,omitempty"`   // which role that was, so it is the same one taken off later
	RestrictionApplied  bool   `json:"restriction_applied,omitempty"`   // the SS Restricted (Frozen) role this ticket gave the player
	HadPriorRestriction bool   `json:"had_prior_restriction,omitempty"` // they held the role already, so it is not taken off on close
}
