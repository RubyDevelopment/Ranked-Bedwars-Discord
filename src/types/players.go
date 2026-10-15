package types

// CodeResult is the outcome of checking a typed code.
type CodeResult int

// GuildMember represents a Discord guild member from REST API
type GuildMember struct {
	Bio    *string `json:"bio"`
	Avatar *string `json:"avatar"`
	Banner *string `json:"banner"`
}
