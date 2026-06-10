package notify

import "github.com/inful/madhatter/internal/notify/channels"

// NewStaticResolver returns a channelResolver that maps channel names
// to the given channel implementations. Use it from server.go at
// startup to wire the worker's channel registry.
func NewStaticResolver(chans ...channels.Channel) channelResolver {
	m := make(map[string]channels.Channel, len(chans))
	for _, c := range chans {
		m[c.Name()] = c
	}
	return staticChannelResolver{channels: m}
}
