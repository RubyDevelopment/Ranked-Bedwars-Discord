package division

import (
	"encoding/json"
	"log"
	"time"

	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/models/guild"
	"github.com/RubyDevelopment/Ranked-Bedwars-Database/src/sdk"
	"github.com/bwmarrin/discordgo"

	"ranked-bedwars/src/database"
)

// Votes are stored in the database (guild_division_votes) every time they change, and the open ones are loaded back when a press or
// a new vote finds one missing from memory, so a restart loses nothing. Closed votes stay in the table as history.

// row is the vote as the table stores it (status "" = still open).
func (v *vote) row(status, by string) *guild.GuildDivisionVotesModel {
	eligible, _ := json.Marshal(v.eligible)
	votes, _ := json.Marshal(v.votes)
	stats, _ := json.Marshal(v.stats)
	r := &guild.GuildDivisionVotesModel{
		Division: v.div.Key, MessageID: v.messageID, ChannelID: v.channelID, LogChannelID: v.logChan,
		TargetID: v.targetID, TargetName: v.name, NominatorID: v.nominator, Reason: v.reason,
		Eligible: eligible, Votes: votes, HasImage: v.image, Stats: stats, Status: guild.DivisionVoteOpen,
	}
	r.ID = v.dbID
	if status != "" {
		now := time.Now()
		r.Status, r.ClosedBy, r.ClosedAt = status, by, &now
	}
	return r
}

// save writes the vote to the database. A failure is logged and the vote carries on in memory.
func (v *vote) save(status, by string) {
	svc := database.Get()
	if svc == nil {
		return
	}
	if v.dbID == 0 {
		created, err := svc.DivisionVotes.Create(sdk.SystemActor, v.guildID, v.row(status, by))
		if err != nil {
			log.Printf("division vote not stored for guild %s: %v", v.guildID, err)
			return
		}
		v.dbID = created.ID
		return
	}
	if err := svc.DivisionVotes.Save(sdk.SystemActor, v.guildID, v.row(status, by)); err != nil {
		log.Printf("division vote %d not saved for guild %s: %v", v.dbID, v.guildID, err)
	}
}

// fromRow rebuilds an open vote; nil when the row cannot be read.
func fromRow(guildID string, r guild.GuildDivisionVotesModel) *vote {
	d, ok := Find(r.Division)
	if !ok {
		return nil
	}
	v := &vote{
		dbID: r.ID, div: d, guildID: guildID, channelID: r.ChannelID, messageID: r.MessageID, logChan: r.LogChannelID,
		targetID: r.TargetID, name: r.TargetName, nominator: r.NominatorID, reason: r.Reason, image: r.HasImage,
		eligible: map[string]string{}, votes: map[string]bool{}, stats: []*discordgo.MessageEmbedField{},
	}
	if json.Unmarshal(r.Eligible, &v.eligible) != nil || json.Unmarshal(r.Votes, &v.votes) != nil || json.Unmarshal(r.Stats, &v.stats) != nil {
		return nil
	}
	return v
}

// loadOpen brings the guild's open votes back from the database into memory (the ones already there are left alone).
func loadOpen(guildID string) {
	svc := database.Get()
	if svc == nil {
		return
	}
	rows, err := svc.DivisionVotes.Open(guildID)
	if err != nil {
		log.Printf("open division votes of guild %s not loaded: %v", guildID, err)
		return
	}
	open.mu.Lock()
	defer open.mu.Unlock()
	for _, r := range rows {
		if _, ok := open.by[r.MessageID]; ok {
			continue
		}
		if v := fromRow(guildID, r); v != nil {
			open.by[v.messageID] = v
		}
	}
}
