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

// ResponseRule points at the saved message that answers.
type ResponseRule struct {
	MessageID string `json:"message_id"`
}
