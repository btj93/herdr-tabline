package herdrapi

import "context"

type Client interface {
	Snapshot(context.Context) (Snapshot, error)
	ProcessInfo(context.Context, string) (ProcessInfo, error)
	RenameTab(context.Context, string, string) error
	// Subscribe opens a dedicated connection and streams envelopes until ctx ends.
	Subscribe(context.Context, []string) (<-chan Event, error)
	Close() error
}
