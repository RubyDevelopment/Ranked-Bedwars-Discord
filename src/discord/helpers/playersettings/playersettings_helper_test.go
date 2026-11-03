package playersettings

import (
	"strings"
	"testing"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/bwmarrin/discordgo"
)

func allAllowed() map[string]bool {
	allowed := map[string]bool{}
	for _, st := range Settings {
		allowed[st.ID] = true
	}
	return allowed
}

// countComponents is how Discord counts a message: every component, including a section's text and accessory.
func countComponents(c discordgo.MessageComponent) int {
	switch v := c.(type) {
	case discordgo.Container:
		n := 1
		for _, child := range v.Components {
			n += countComponents(child)
		}
		return n
	case discordgo.Section:
		return 1 + len(v.Components) + 1
	default:
		return 1
	}
}

func container(t *testing.T, c *models.PlayerConfigsModel, allowed map[string]bool) discordgo.Container {
	t.Helper()
	panel := Panel(c, allowed)
	if len(panel) != 1 {
		t.Fatalf("the answer is %d components, want one container", len(panel))
	}
	box, ok := panel[0].(discordgo.Container)
	if !ok {
		t.Fatalf("the answer is a %T, want a container", panel[0])
	}
	return box
}

// The title and note sit beside the player's head, then every switch is a section: its text on the left, an icon-only button on the right.
func TestPanelLayout(t *testing.T) {
	box := container(t, &models.PlayerConfigsModel{Username: "Steve", IsStatsVisible: true}, allAllowed())

	var sections []discordgo.Section
	for _, child := range box.Components {
		if s, ok := child.(discordgo.Section); ok {
			sections = append(sections, s)
		}
	}
	if len(sections) != len(Settings)+1 {
		t.Fatalf("%d sections, want a header and one per setting (%d)", len(sections), len(Settings)+1)
	}

	head := sections[0]
	if _, ok := head.Accessory.(discordgo.Thumbnail); !ok {
		t.Errorf("the title's accessory is a %T, want the player's head", head.Accessory)
	}
	if text := head.Components[0].(discordgo.TextDisplay).Content; !strings.Contains(text, "Steve") {
		t.Errorf("the title does not name the player: %q", text)
	}

	for n, st := range Settings {
		sec := sections[n+1]
		btn, ok := sec.Accessory.(discordgo.Button)
		if !ok {
			t.Fatalf("%s: accessory is a %T, want a button", st.ID, sec.Accessory)
		}
		if btn.CustomID != idPrefix+st.ID || btn.Label != "" || btn.Emoji == nil || btn.Emoji.Name != st.Emoji {
			t.Errorf("%s: button should be the icon %s with id %s and no text, got %+v", st.ID, st.Emoji, idPrefix+st.ID, btn)
		}
		// The button's colour is the state: the text is only the name and what the setting does.
		text := sec.Components[0].(discordgo.TextDisplay).Content
		if want := "**" + st.Emoji + " " + st.Label + "**\n> " + quote(st.Detail); text != want {
			t.Errorf("%s: text beside the button is %q, want %q", st.ID, text, want)
		}
	}
}

// Green when on, grey when off, disabled and marked when the guild locks it.
func TestPanelButtonStatesAndLocks(t *testing.T) {
	allowed := allAllowed()
	allowed["elo"] = false
	box := container(t, &models.PlayerConfigsModel{Username: "Steve", IsStatsVisible: true, IsEloVisible: true}, allowed)

	found := map[string]discordgo.Section{}
	for _, child := range box.Components {
		if s, ok := child.(discordgo.Section); ok {
			if b, ok := s.Accessory.(discordgo.Button); ok {
				found[b.CustomID] = s
			}
		}
	}
	stats := found[idPrefix+"stats"].Accessory.(discordgo.Button)
	if stats.Style != discordgo.SuccessButton {
		t.Errorf("stats is on, its button should be green, got style %d", stats.Style)
	}
	autowarp := found[idPrefix+"autowarp"].Accessory.(discordgo.Button)
	if autowarp.Style != discordgo.SecondaryButton {
		t.Errorf("autowarp is off, its button should be grey, got style %d", autowarp.Style)
	}
	elo := found[idPrefix+"elo"]
	if !elo.Accessory.(discordgo.Button).Disabled {
		t.Error("a locked setting's button should be disabled")
	}
	if !strings.Contains(elo.Components[0].(discordgo.TextDisplay).Content, "🔒") {
		t.Error("a locked setting should show the lock")
	}
}

// Discord allows 40 components in a message. Adding settings past what fits has to be a deliberate decision (a second message), not a
// silent failure when someone runs /settings.
func TestPanelFitsInOneMessage(t *testing.T) {
	box := container(t, &models.PlayerConfigsModel{Username: "Steve"}, allAllowed())
	if n := countComponents(box); n > maxComponents {
		t.Errorf("%d settings make %d components, Discord allows %d: split the answer before adding more", len(Settings), n, maxComponents)
	}
}

// Every setting explains what On and Off do and gives an example.
func TestSettingsAreExplainedWithAnExample(t *testing.T) {
	for _, st := range Settings {
		for _, part := range []string{"**On:**", "**Off:**", "*Example:*"} {
			if !strings.Contains(st.Detail, part) {
				t.Errorf("%s: the explanation lacks %s", st.ID, part)
			}
		}
	}
}

// Discord allows 4000 characters of text in a message in all. The explanations make the panel long, so this fails before a setting added
// later can silently break /settings: shorten the explanations, or split the answer.
func TestPanelTextFitsDiscordLimit(t *testing.T) {
	box := container(t, &models.PlayerConfigsModel{Username: "A_Sixteen_Name_X"}, allAllowed())
	total := 0
	for _, child := range box.Components {
		if s, ok := child.(discordgo.Section); ok {
			for _, c := range s.Components {
				total += len([]rune(c.(discordgo.TextDisplay).Content))
			}
		}
	}
	if total > 4000 {
		t.Errorf("%d characters of text for %d settings, Discord allows 4000", total, len(Settings))
	}
	t.Logf("%d of 4000 characters used by %d settings", total, len(Settings))
}

// Each setting has its own icon, so the buttons can be told apart without text.
func TestSettingIconsAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, st := range Settings {
		if other, dup := seen[st.Emoji]; dup {
			t.Errorf("%s and %s share the icon %s", st.ID, other, st.Emoji)
		}
		seen[st.Emoji] = st.ID
	}
}
