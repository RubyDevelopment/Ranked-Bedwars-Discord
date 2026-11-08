package helpers

// ScreenshareFrozenRole returns the SS Restricted (Frozen) role the guild's screenshare panel is set up with, or "". The screenshare
// package sets it; the queue reads it, since a player who is frozen for a screenshare cannot queue.
var ScreenshareFrozenRole func(guildID string) string
