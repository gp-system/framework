package realtime

import (
	"strings"
	"testing"

	"github.com/gp-system/auth/rbac"
)

func TestTopicAuth_UnregisteredTopicDenied(t *testing.T) {
	topics := newTopicAuth()
	if topics.authorize("announcements", &rbac.Identity{Subject: "u1"}) {
		t.Fatal("expected an unregistered topic to be denied")
	}
}

func TestTopicAuth_RegisteredCallback(t *testing.T) {
	topics := newTopicAuth()
	topics.Allow("admin-alerts", func(id *rbac.Identity) bool { return id.HasRole("admin") })

	if topics.authorize("admin-alerts", &rbac.Identity{Subject: "u1", Roles: []string{"user"}}) {
		t.Fatal("expected non-admin to be denied")
	}
	if !topics.authorize("admin-alerts", &rbac.Identity{Subject: "u2", Roles: []string{"admin"}}) {
		t.Fatal("expected admin to be allowed")
	}
}

func TestTopicAuth_AllowPrefix(t *testing.T) {
	topics := newTopicAuth()
	topics.AllowPrefix("namespace:", func(id *rbac.Identity, topic string) bool {
		ns, _ := strings.CutPrefix(topic, "namespace:")
		return id.HasRole("admin") || ns == "acme"
	})

	if !topics.authorize("namespace:acme", &rbac.Identity{Subject: "u1", Roles: []string{"user"}}) {
		t.Fatal("expected namespace:acme to be allowed for a non-admin matching the special-cased namespace")
	}
	if topics.authorize("namespace:other", &rbac.Identity{Subject: "u1", Roles: []string{"user"}}) {
		t.Fatal("expected namespace:other to be denied for a non-admin")
	}
	if !topics.authorize("namespace:other", &rbac.Identity{Subject: "u2", Roles: []string{"admin"}}) {
		t.Fatal("expected namespace:other to be allowed for an admin")
	}
	if topics.authorize("unrelated-topic", &rbac.Identity{Subject: "u1", Roles: []string{"admin"}}) {
		t.Fatal("expected a topic matching no prefix and no exact Allow to be denied")
	}
}

func TestTopicAuth_ExactAllowWinsOverPrefix(t *testing.T) {
	topics := newTopicAuth()
	topics.AllowPrefix("namespace:", func(id *rbac.Identity, topic string) bool { return false })
	topics.Allow("namespace:special", func(id *rbac.Identity) bool { return true })

	if !topics.authorize("namespace:special", &rbac.Identity{Subject: "u1"}) {
		t.Fatal("expected an exact Allow match to take priority over a matching AllowPrefix")
	}
}
