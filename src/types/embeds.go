package types

import (
	"github.com/bwmarrin/discordgo"
)

type EmbedAuthorOptions struct {
	Name    interface{}
	IconURL interface{}
}

type PresetOptions struct {
	Title       interface{}
	Description interface{}
	Color       int
	Image       interface{}
	Thumbnail   interface{}
	Timestamp   interface{}
	Author      *EmbedAuthorOptions
	FooterText  interface{}
	FooterIcon  interface{}
	Fields      []*discordgo.MessageEmbedField
}

// ResponseRule says what answers: a saved message (MessageID) or a reply built on the dashboard itself (Reply).
type ResponseRule struct {
	MessageID string         `json:"message_id,omitempty"`
	Reply     *ResponseReply `json:"reply,omitempty"`
	// Enabled is only ever stored as false: the rule is switched off but kept, and the next broader rule answers.
	Enabled *bool `json:"enabled,omitempty"`
}

// Usable reports whether the rule names something to answer with.
func (r ResponseRule) Usable() bool {
	return (r.Enabled == nil || *r.Enabled) && (r.MessageID != "" || r.Reply != nil)
}

// ResponseReply is a reply built in the dashboard's response builder, in the shape custom commands use: a plain
// message (Content), classic embeds, or Components V2.
type ResponseReply struct {
	Type       string       `json:"type"` // message, embed or component
	Content    string       `json:"content"`
	Text       string       `json:"text"`
	Buttons    []LinkButton `json:"buttons"`
	Embeds     []Embed      `json:"embeds"`
	Components []V2Item     `json:"components"`
}
