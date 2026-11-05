package division

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"maps"
	"strings"
	"sync"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/discord/helpers/banner"
	"ranked-bedwars/src/types"
)

// A division vote is a message in the division's vote channel: the player's stats banner in an embed and Approve / Deny buttons that
// the division's members, specs and managers answer. It never gives the role: when it ends, the result goes to the vote log channel
// and a manager adds the player with /<division> add. A voter can change their vote and check it with My Vote.
//
// ponytail: open votes live in memory, so a bot restart closes them (a press then says so); store them in the database if restarts
// during a vote turn out to matter.

const votePrefix = "divvote|" // divvote|yes|pugs, divvote|no|pugs, divvote|mine|pugs, divvote|cancel|pugs

type vote struct {
	mu        sync.Mutex
	div       Div
	guildID   string
	channelID string
	messageID string
	logChan   string
	targetID  string
	name      string // the target's username
	nominator string
	reason    string
	eligible  map[string]bool // who may vote, fixed when the vote starts
	votes     map[string]bool // voter -> approves
	need      int
	image     bool                           // the stats banner is attached as stats.png
	stats     []*discordgo.MessageEmbedField // a plain stats summary, when there is no banner
	closed    bool
}

var open = struct {
	mu sync.Mutex
	by map[string]*vote // message id -> the open vote
}{by: map[string]*vote{}}

func init() { helpers.RegisterComponentHandler(handleVote) }

// votesNeeded is half of the eligible voters, rounded up (at least one).
func votesNeeded(eligible int) int { return max(1, (eligible+1)/2) }

// outcome is "passed" once the approvals reach need, "failed" when even every voter left approving could not reach it, else "".
func outcome(yes, no, eligible, need int) string {
	switch {
	case yes >= need:
		return "passed"
	case eligible-no < need:
		return "failed"
	}
	return ""
}

func (v *vote) counts() (yes, no int) {
	for _, approves := range v.votes {
		if approves {
			yes++
		} else {
			no++
		}
	}
	return yes, no
}

func (v *vote) link() string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", v.guildID, v.channelID, v.messageID)
}

// embed is the vote message while open (status "") and its result once over ("passed", "failed" or "cancelled" by <@id>).
func (v *vote) embed(status, by string) *discordgo.MessageEmbed {
	yes, no := v.counts()
	preset, title, result := "default", fmt.Sprintf("%s Vote · %s", v.div.Label, v.name), ""
	switch status {
	case "passed":
		preset, result = "success", fmt.Sprintf("**Approved** by vote. No role was added: a manager can add them with `/%s add`.", v.div.Key)
	case "failed":
		preset, result = "error", "**Denied** by vote."
	case "cancelled":
		preset, result = "info", "**Cancelled** by <@"+by+">."
	}
	desc := fmt.Sprintf("<@%s> was nominated for **%s** by <@%s>.\n**Reason:** %s\n\n**%d** approvals of **%d** eligible voters are needed.", v.targetID, v.div.Label, v.nominator, v.reason, v.need, len(v.eligible))
	if result != "" {
		desc += "\n\n" + result
	}
	fields := append([]*discordgo.MessageEmbedField{}, v.stats...)
	fields = append(fields,
		&discordgo.MessageEmbedField{Name: "Approve", Value: fmt.Sprint(yes), Inline: true},
		&discordgo.MessageEmbedField{Name: "Deny", Value: fmt.Sprint(no), Inline: true})
	opts := types.PresetOptions{Title: title, Description: desc, Fields: fields, Thumbnail: helpers.MinecraftBustAvatarURL(v.name)}
	if v.image {
		opts.Image = "attachment://stats.png"
	}
	return helpers.Preset(preset, opts)
}

func (v *vote) buttons() []discordgo.MessageComponent {
	yes, no := v.counts()
	id := func(action string) string { return votePrefix + action + "|" + v.div.Key }
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		&discordgo.Button{Label: fmt.Sprintf("Approve (%d)", yes), Style: discordgo.SuccessButton, CustomID: id("yes")},
		&discordgo.Button{Label: fmt.Sprintf("Deny (%d)", no), Style: discordgo.DangerButton, CustomID: id("no")},
		&discordgo.Button{Label: "My Vote", Style: discordgo.SecondaryButton, CustomID: id("mine")},
		&discordgo.Button{Label: "Cancel Vote", Style: discordgo.SecondaryButton, CustomID: id("cancel")},
	}}}
}

// ============================================================================
// Starting a vote
// ============================================================================

var errNotRegistered = errors.New("player is not registered")

// statsCard draws the target's stats banner, the way /stats does. With no banner style the guild made it returns a plain summary
// instead, and with no active season neither (the vote still runs).
func statsCard(s *discordgo.Session, guildID string, target, nominator *discordgo.User) (png []byte, name string, summary []*discordgo.MessageEmbedField, err error) {
	view, err := database.Must().Player.Get(guildID, target.ID)
	if err != nil {
		if sdk.IsNotFound(err) {
			err = errNotRegistered
		}
		return nil, "", nil, err
	}
	name = target.Username
	if view.Config != nil && view.Config.Username != "" {
		name = view.Config.Username
	}
	seasonKey, seasonID, err := helpers.ActiveSeasonKey(guildID)
	if err != nil || seasonKey == "" {
		return nil, name, nil, err
	}
	playerID := sdk.UintToString(view.Player.ID)
	stats, err := database.Must().Stats.GetOrCreate(guildID, playerID, seasonID)
	if err != nil {
		return nil, name, nil, err
	}
	position := 0
	if rank, err := database.Must().Stats.Rank(guildID, playerID, seasonID); err == nil {
		position = rank.Position
	}
	doc := banner.LoadBanners(guildID)
	viewer := banner.BannerViewer(target.ID, banner.LookupMember(s, guildID, target.ID))
	if chosen := doc.Choose("stats", viewer, banner.ChosenBanner(view.Config, "stats")); chosen != nil {
		vars := banner.GuildVars(s, guildID, nominator)
		maps.Copy(vars, banner.StatsVars(target, view.Config, stats, position, seasonKey, seasonID))
		img, rerr := banner.RenderBanner(chosen, vars)
		if rerr == nil {
			return img, name, nil, nil
		}
		log.Printf("division vote banner for guild %s: %v", guildID, rerr)
	}
	return nil, name, statSummary(stats), nil
}

func statSummary(st *models.PlayerStatsModel) []*discordgo.MessageEmbedField {
	return []*discordgo.MessageEmbedField{
		{Name: "ELO", Value: fmt.Sprint(st.Elo), Inline: true},
		{Name: "Rank", Value: st.Rank, Inline: true},
		{Name: "Record", Value: fmt.Sprintf("%d W · %d L", st.Wins, st.Losses), Inline: true},
		{Name: "K/D", Value: banner.Ratio(st.Kills, st.Deaths), Inline: true},
	}
}

// startVote posts the vote for the target in the division's vote channel and returns the answer to the nominator.
func startVote(s *discordgo.Session, i *discordgo.InteractionCreate, d Div, cfg Config, target *discordgo.User, why string) *discordgo.MessageEmbed {
	if cfg.VoteChannel == "" {
		return notConfiguredEmbed(d, SetVotes)
	}
	if cfg.Member == "" && cfg.Spec == "" && cfg.Manager == "" {
		return errorEmbed("Not Configured", "The "+d.Label+" member, spec and manager roles are not set, so nobody could vote. An administrator can set them with `/divisions set-role`.")
	}
	if why == "" {
		why = "No reason given."
	}
	if member, err := helpers.FetchGuildMember(s, i.GuildID, target.ID); err != nil {
		return errorEmbed("Player Not Found", "<@"+target.ID+"> is not in this server.")
	} else if helpers.MemberHasRole(member, cfg.Member) {
		return errorEmbed("Already Added", fmt.Sprintf("<@%s> is already in %s.", target.ID, d.Label))
	}

	nominator := i.Member.User
	png, name, summary, err := statsCard(s, i.GuildID, target, nominator)
	if errors.Is(err, errNotRegistered) {
		return errorEmbed("Player Not Registered", "<@"+target.ID+"> is not registered, so there are no stats to vote on. They must run `/register` first.")
	}
	if err != nil {
		return unexpectedEmbed()
	}
	members, err := guildMembers(s, i.GuildID)
	if err != nil {
		return unexpectedEmbed()
	}
	eligible := map[string]bool{}
	for _, roleID := range []string{cfg.Member, cfg.Spec, cfg.Manager} {
		if roleID == "" {
			continue
		}
		for _, id := range holders(members, roleID) {
			if id != target.ID {
				eligible[id] = true
			}
		}
	}
	if len(eligible) == 0 {
		return errorEmbed("No Voters", "Nobody holds the "+d.Label+" roles, so nobody could vote.")
	}

	v := &vote{
		div: d, guildID: i.GuildID, channelID: cfg.VoteChannel, logChan: cfg.LogChannel, targetID: target.ID, name: name,
		nominator: helpers.InteractionUserID(i), reason: why, eligible: eligible, votes: map[string]bool{},
		need: votesNeeded(len(eligible)), image: png != nil, stats: summary,
	}
	msg := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{v.embed("", "")}, Components: v.buttons()}
	if png != nil {
		msg.Files = []*discordgo.File{{Name: "stats.png", ContentType: "image/png", Reader: bytes.NewReader(png)}}
	}

	// Held until the vote is registered, so a press cannot arrive for a message that is not known yet.
	open.mu.Lock()
	defer open.mu.Unlock()
	for _, other := range open.by {
		if other.guildID == v.guildID && other.div.Key == d.Key && other.targetID == v.targetID {
			return errorEmbed("Vote Already Open", fmt.Sprintf("There is already a %s vote for <@%s>: %s", d.Label, target.ID, other.link()))
		}
	}
	sent, err := helpers.SendChannelMessage(s, i.GuildID, cfg.VoteChannel, msg)
	if err != nil {
		return errorEmbed("Vote Not Posted", "The vote could not be posted in <#"+cfg.VoteChannel+">. Check that I can send messages, embed links and attach files there.")
	}
	v.messageID = sent.ID
	open.by[sent.ID] = v
	return helpers.Preset("success", types.PresetOptions{
		Title:       "Vote Started",
		Description: fmt.Sprintf("The %s vote for <@%s> is open in <#%s>.\n%s", d.Label, target.ID, cfg.VoteChannel, v.link()),
		Thumbnail:   helpers.MinecraftBustAvatarURL(name),
	})
}

// ============================================================================
// Voting
// ============================================================================

// handleVote counts an Approve or Deny press, answers My Vote and Cancel Vote, and ends the vote when it is decided.
func handleVote(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) bool {
	if !strings.HasPrefix(customID, votePrefix) {
		return false
	}
	parts := strings.Split(customID, "|")
	if len(parts) != 3 || i.Message == nil {
		return false
	}
	action := parts[1]
	respond := func(t discordgo.InteractionResponseType, d *discordgo.InteractionResponseData) {
		_ = helpers.InteractionRespond(s, i.Interaction, &discordgo.InteractionResponse{Type: t, Data: d})
	}
	say := func(text string) {
		respond(discordgo.InteractionResponseChannelMessageWithSource, &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, Content: text})
	}

	open.mu.Lock()
	v := open.by[i.Message.ID]
	open.mu.Unlock()
	if v == nil {
		// Unknown (it ended, or the bot restarted): keep the message, take the buttons away.
		respond(discordgo.InteractionResponseUpdateMessage, &discordgo.InteractionResponseData{Embeds: i.Message.Embeds, Components: []discordgo.MessageComponent{}})
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Flags: discordgo.MessageFlagsEphemeral, Content: "This vote is no longer open (it ended, or the bot restarted). Ask a manager to start a new one."})
		return true
	}

	uid := helpers.InteractionUserID(i)
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		say("This vote is already over.")
		return true
	}
	update := func(status, by string) {
		components := []discordgo.MessageComponent{}
		if status == "" {
			components = v.buttons()
		}
		respond(discordgo.InteractionResponseUpdateMessage, &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{v.embed(status, by)}, Components: components})
	}

	switch action {
	case "mine":
		approves, voted := v.votes[uid]
		switch {
		case voted:
			choice := "Deny"
			if approves {
				choice = "Approve"
			}
			say(fmt.Sprintf("You voted **%s** on the %s vote for <@%s>. Press the other button to change it.", choice, v.div.Label, v.targetID))
		case v.eligible[uid]:
			say(fmt.Sprintf("You have not voted yet on the %s vote for <@%s>.", v.div.Label, v.targetID))
		default:
			say(fmt.Sprintf("You cannot vote on the %s vote for <@%s>: only %s members, specs and managers can.", v.div.Label, v.targetID, v.div.Label))
		}
		return true

	case "cancel":
		cfg, err := Load(v.guildID, v.div)
		if err != nil || !IsManager(s, i, cfg) {
			say("Only " + v.div.Label + " managers can cancel a vote.")
			return true
		}
		v.finish(s, "cancelled", uid, update)
		return true

	case "yes", "no":
		if !v.eligible[uid] {
			say(fmt.Sprintf("Only %s members, specs and managers can vote here (and not on their own nomination).", v.div.Label))
			return true
		}
		v.votes[uid] = action == "yes"
		yes, no := v.counts()
		if result := outcome(yes, no, len(v.eligible), v.need); result != "" {
			v.finish(s, result, "", update)
		} else {
			update("", "")
		}
		return true
	}
	return false
}

// finish ends the vote (the caller holds v.mu): the message shows the result, and the result and who voted go to the log channel.
func (v *vote) finish(s *discordgo.Session, status, by string, update func(status, by string)) {
	v.closed = true
	open.mu.Lock()
	delete(open.by, v.messageID)
	open.mu.Unlock()
	update(status, by)

	if v.logChan == "" {
		return
	}
	var approved, denied []string
	for id, approves := range v.votes {
		if approves {
			approved = append(approved, id)
		} else {
			denied = append(denied, id)
		}
	}
	e := v.embed(status, by)
	e.Image = nil
	e.Fields = append(e.Fields,
		&discordgo.MessageEmbedField{Name: "Approved by", Value: mentions(approved)},
		&discordgo.MessageEmbedField{Name: "Denied by", Value: mentions(denied)},
		&discordgo.MessageEmbedField{Name: "Vote", Value: v.link()})
	_, _ = helpers.SendChannelMessage(s, v.guildID, v.logChan, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{e}})
}
