package realtime

import (
	"context"
	"strings"

	"github.com/centrifugal/centrifuge"

	"github.com/gp-system/auth"
	"github.com/gp-system/auth/rbac"
	"github.com/gp-system/notify/broadcast"
)

// newNode builds the gateway's centrifuge.Node: JWT authentication at
// connect (the client sends its token in the connect frame; there is no
// separate auth endpoint, because v1's only private channel is a
// connection's own user channel, computed from the token's subject), a
// server-side subscription to that user channel so clients need no
// subscribe logic of their own, topic-channel authorization through the
// TopicAuth callbacks registered by RegisterFunc, and client-to-server RPC
// dispatch through the RPCRegistry callbacks registered by WithRPC.
func newNode(cfg Config, topics *TopicAuth, rpc *RPCRegistry) (*centrifuge.Node, error) {
	node, err := broadcast.NewNode(cfg.BroadcastValkey(), centrifuge.Config{})
	if err != nil {
		return nil, err
	}

	issuer := auth.NewTokenIssuer[auth.DefaultClaims](cfg.JWT)
	topicPrefix := cfg.Broadcast.ChannelPrefix + ":topic:"

	node.OnConnecting(func(ctx context.Context, e centrifuge.ConnectEvent) (centrifuge.ConnectReply, error) {
		claims, err := issuer.Parse(e.Token)
		if err != nil {
			return centrifuge.ConnectReply{}, centrifuge.ErrorUnauthorized
		}
		id := &rbac.Identity{
			Subject:     claims.Subject,
			Username:    claims.Username,
			Roles:       claims.Roles,
			Permissions: claims.Permissions,
		}
		return centrifuge.ConnectReply{
			Context:     rbac.WithIdentity(ctx, id),
			Credentials: &centrifuge.Credentials{UserID: claims.Subject},
			Subscriptions: map[string]centrifuge.SubscribeOptions{
				broadcast.UserChannel(cfg.Broadcast.ChannelPrefix, claims.Subject): {},
			},
		}, nil
	})

	node.OnConnect(func(client *centrifuge.Client) {
		client.OnSubscribe(func(e centrifuge.SubscribeEvent, cb centrifuge.SubscribeCallback) {
			topic, ok := strings.CutPrefix(e.Channel, topicPrefix)
			if !ok {
				cb(centrifuge.SubscribeReply{}, centrifuge.ErrorUnknownChannel)
				return
			}
			id := rbac.FromContext(client.Context())
			if !topics.authorize(topic, id) {
				cb(centrifuge.SubscribeReply{}, centrifuge.ErrorPermissionDenied)
				return
			}
			cb(centrifuge.SubscribeReply{}, nil)
		})

		client.OnRPC(func(e centrifuge.RPCEvent, cb centrifuge.RPCCallback) {
			fn, ok := rpc.lookup(e.Method)
			if !ok {
				cb(centrifuge.RPCReply{}, centrifuge.ErrorMethodNotFound)
				return
			}
			id := rbac.FromContext(client.Context())
			out, err := fn(client.Context(), id, e.Data)
			if err != nil {
				cb(centrifuge.RPCReply{}, rpcError(err))
				return
			}
			cb(centrifuge.RPCReply{Data: out}, nil)
		})
	})

	return node, nil
}
