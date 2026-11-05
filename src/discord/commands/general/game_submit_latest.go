package general

import (
	"context"
	"sync"
	"time"

	"ranked-bedwars/src/discord/helpers"
	"ranked-bedwars/src/types"

	"github.com/bwmarrin/discordgo"
)

// A game has one submission that counts: the newest. When a player submits again, the one before is cancelled: if it is still being read it
// stops, and if its answer is already there, that message is edited to say it was replaced and its buttons are gone. A button of an older
// answer that is still around (after a restart, say) is refused by its sequence number.

type submission struct {
	seq    int64
	cancel context.CancelFunc
	msg    *discordgo.Message // the answer given, once it is
}

var submissions = struct {
	sync.Mutex
	byGame map[uint]*submission
}{byGame: map[uint]*submission{}}

// beginSubmission makes a new submission the one that counts for the game and cancels the one before it.
func beginSubmission(s *discordgo.Session, gameID uint) (context.Context, *submission) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	sub := &submission{seq: time.Now().UnixMilli(), cancel: cancel}
	submissions.Lock()
	prev := submissions.byGame[gameID]
	submissions.byGame[gameID] = sub
	var old *discordgo.Message
	if prev != nil {
		prev.cancel()
		old = prev.msg
	}
	submissions.Unlock()
	if old != nil {
		go markReplaced(s, old)
	}
	return ctx, sub
}

// isLatest reports whether no newer submission has begun for the game.
func (sub *submission) isLatest(gameID uint) bool {
	submissions.Lock()
	defer submissions.Unlock()
	return submissions.byGame[gameID] == sub
}

// answered remembers the message that answered the submission, so a newer one can replace it.
func (sub *submission) answered(msg *discordgo.Message) {
	submissions.Lock()
	sub.msg = msg
	submissions.Unlock()
}

// latestSeq is the sequence number of the game's current submission; ok is false when none is known (the bot restarted since).
func latestSeq(gameID uint) (int64, bool) {
	submissions.Lock()
	defer submissions.Unlock()
	if sub := submissions.byGame[gameID]; sub != nil {
		return sub.seq, true
	}
	return 0, false
}

func submissionReplacedEmbed() *discordgo.MessageEmbed {
	return helpers.Preset("info", types.PresetOptions{
		Title:       "Replaced by a newer screenshot",
		Description: "A newer screenshot was submitted for this game, so this one was cancelled. Use the latest message.",
		Thumbnail:   helpers.ThumbFlag,
	})
}

func markReplaced(s *discordgo.Session, msg *discordgo.Message) {
	embeds, comps := []*discordgo.MessageEmbed{submissionReplacedEmbed()}, []discordgo.MessageComponent{}
	_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: msg.ID, Channel: msg.ChannelID, Embeds: &embeds, Components: &comps})
}
