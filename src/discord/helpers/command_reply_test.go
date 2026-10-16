package helpers

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"ranked-bedwars/src/types"
)

func TestCustomReplyPayloadBuildsEmbed(t *testing.T) {
	r := runner{vars: map[string]string{"player.name": "Wiggels"}}
	payload := r.replyPayload(reply{
		Type: "embed",
		Embeds: []types.Embed{{
			Title:       "Stats for {{player.name}}",
			Description: "Custom response",
			Color:       "#5865f2",
		}},
	})

	embeds, ok := payload["embeds"].([]map[string]any)
	if !ok || len(embeds) != 1 {
		t.Fatalf("expected one embed, got %#v", payload["embeds"])
	}
	if embeds[0]["title"] != "Stats for Wiggels" || embeds[0]["description"] != "Custom response" {
		t.Fatalf("embed variables were not filled: %#v", embeds[0])
	}
}

func TestCustomReplyPayloadBuildsComponentsV2(t *testing.T) {
	r := runner{vars: map[string]string{"player.name": "Wiggels"}}
	payload := r.replyPayload(reply{
		Type: "component",
		Components: []types.V2Item{
			{Type: "text", Content: "Hello {{player.name}}"},
			{Type: "container", Accent: "#55ffff", Children: []types.V2Item{{Type: "text", Content: "Welcome"}}},
		},
	})

	if payload["flags"] != V2Flag {
		t.Fatalf("expected Components V2 flag %d, got %#v", V2Flag, payload["flags"])
	}
	components, ok := payload["components"].([]map[string]any)
	if !ok || len(components) != 2 {
		t.Fatalf("expected two components, got %#v", payload["components"])
	}
	if components[0]["type"] != 10 || components[0]["content"] != "Hello Wiggels" {
		t.Fatalf("unexpected text component: %#v", components[0])
	}
	if components[1]["type"] != 17 {
		t.Fatalf("expected a container component, got %#v", components[1])
	}
}

func TestCustomStepRoleCheckUsesConfiguredRole(t *testing.T) {
	user := &discordgo.User{ID: "user-id"}
	r := runner{
		i: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
			GuildID: "guild-id",
			Member:  &discordgo.Member{User: user, Roles: []string{"staff-role"}},
		}},
		invoker: user,
		e: &entry{cfg: types.CommandConfig{
			RequiredRoles: map[string]types.RequirementSlot{"staff": {ID: "staff-role"}},
		}},
	}

	message, stopped := r.exec(step{Type: "check_role", Name: "staff"})
	if stopped || message != "" {
		t.Fatalf("expected configured role check to continue, got message %q, stopped %v", message, stopped)
	}

	r.i.Member.Roles = nil
	message, stopped = r.exec(step{Type: "check_role", Name: "staff", Message: "Staff only"})
	if !stopped || message != "Staff only" {
		t.Fatalf("expected role check to stop with custom message, got message %q, stopped %v", message, stopped)
	}
}

func TestCustomStepConditionUsesLoadedVariables(t *testing.T) {
	r := runner{vars: map[string]string{"stats.wins": "12"}}
	if !r.holds(condition{Left: "{{stats.wins}}", Op: "gte", Right: "10"}) {
		t.Fatal("expected condition to compare interpolated variables")
	}
	if r.holds(condition{Left: "{{stats.wins}}", Op: "lt", Right: "10"}) {
		t.Fatal("expected false condition for lower bound")
	}
}
