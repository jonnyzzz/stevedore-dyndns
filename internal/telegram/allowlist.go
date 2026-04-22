// Package telegram runs a minimal Telegram Bot that reports MTProto secret
// activity and accepts /status and /rotate from allow-listed users in
// private chats only. In groups the bot is write-only: it never responds
// to messages, but it can be added so its broadcast messages land there.
package telegram

// AllowedUsers is the hardcoded allow-list of Telegram user IDs permitted
// to issue commands in a DM with the bot. Anyone else is ignored — the bot
// does not acknowledge or reply to non-allow-listed users.
//
// To find your own user ID: send /start to @userinfobot on Telegram.
// Add new IDs by editing this file and redeploying; this is intentionally
// compile-time, not config, so stevedore-param leaks cannot grant access.
var AllowedUsers = []int64{
	// Eugene Petrenko — primary admin.
	// Replace the placeholder below with your actual Telegram user ID.
	0,
}

// IsAllowed reports whether the given Telegram user ID is in AllowedUsers.
func IsAllowed(userID int64) bool {
	for _, id := range AllowedUsers {
		if id == 0 {
			continue
		}
		if id == userID {
			return true
		}
	}
	return false
}
