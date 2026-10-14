package types

// VerifyResult is the engine's answer to a Verify: Status "sent" means the code is in the player's party chat; anything else
// (offline, not_joined, no_bot, no_server, no_profile, connect, error) is why it was not, with Reason in the engine's words.
type VerifyResult struct {
	Status string
	Reason string
}
