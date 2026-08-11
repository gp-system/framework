package realtime

import (
	"strings"
	"sync"

	"github.com/gp-system/auth/rbac"
)

// TopicAuth authorizes client-side subscriptions to shared topic channels
// (broadcast.TopicChannel). A connection's own user channel needs no entry
// here: it is always and only the caller's own subject, subscribed
// server-side on connect.
type TopicAuth struct {
	mu       sync.RWMutex
	allow    map[string]func(id *rbac.Identity) bool
	prefixes []prefixRule
}

// prefixRule is one AllowPrefix registration, checked in registration order
// (first match wins) after an exact Allow match misses.
type prefixRule struct {
	prefix string
	ok     func(id *rbac.Identity, topic string) bool
}

func newTopicAuth() *TopicAuth {
	return &TopicAuth{allow: map[string]func(id *rbac.Identity) bool{}}
}

// Allow registers ok as the authorization check for topic. A topic with no
// registered check (exact match or AllowPrefix) is never subscribable.
func (t *TopicAuth) Allow(topic string, ok func(id *rbac.Identity) bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.allow[topic] = ok
}

// AllowPrefix registers ok as the authorization check for every topic
// starting with prefix, for channel families whose full name is only known
// at runtime (one per row of some dynamic resource: a namespace, a
// repository, a tenant) rather than a small fixed set nameable at startup
// with individual Allow calls. ok receives the full topic string (unlike
// Allow's callback) so it can look up which specific instance the
// subscribing connection is asking for. When more than one prefix matches,
// the one registered first wins; register more specific prefixes first if
// that matters to a given RegisterFunc.
func (t *TopicAuth) AllowPrefix(prefix string, ok func(id *rbac.Identity, topic string) bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prefixes = append(t.prefixes, prefixRule{prefix: prefix, ok: ok})
}

func (t *TopicAuth) authorize(topic string, id *rbac.Identity) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if fn, ok := t.allow[topic]; ok {
		return fn(id)
	}
	for _, rule := range t.prefixes {
		if strings.HasPrefix(topic, rule.prefix) {
			return rule.ok(id, topic)
		}
	}
	return false
}
