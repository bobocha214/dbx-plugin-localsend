package im

// Presence is deliberately kept in memory: saved identities are not live sessions.
// The client polls every 3.5 seconds; allow short network interruptions.
const presenceTimeout int64 = 30000
