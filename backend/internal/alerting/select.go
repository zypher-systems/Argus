package alerting

import "argus/internal/store"

// SelectChannels returns the named channels that should receive event.
// attached == nil means the default set (every channel). An empty slice means none.
func SelectChannels(all []store.Channel, attached []int64, event string) []store.Channel {
	useAll := attached == nil
	allow := make(map[int64]struct{}, len(attached))
	for _, id := range attached {
		allow[id] = struct{}{}
	}
	var out []store.Channel
	for _, ch := range all {
		if !useAll {
			if _, ok := allow[ch.ID]; !ok {
				continue
			}
		}
		if !ch.Allows(event) {
			continue
		}
		out = append(out, ch)
	}
	return out
}

func HookBody(kind string, p Payload) []byte {
	switch kind {
	case store.ChannelSlack:
		return SlackBody(p)
	case store.ChannelDiscord:
		return DiscordBody(p)
	default:
		return WebhookBody(p)
	}
}
