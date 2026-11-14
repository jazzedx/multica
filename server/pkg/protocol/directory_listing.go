package protocol

// PendingWorkKindDirectoryListing asks a daemon that supports it to claim the
// directory listings queued for a runtime. Older daemons heartbeat as for any
// kind, which claims nothing, and the request times out.
const PendingWorkKindDirectoryListing = "directory_listing"
