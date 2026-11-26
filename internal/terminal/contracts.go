package terminal

import "github.com/MaoLikeQvQ/MiProxy-Panel/internal/api"

// These aliases keep terminal state local while sharing the wire contract with the server.
type coreSnapshot = api.CoreStatus
type runtimeOverview = api.Overview
type subscriptionInfo = api.Subscription
type runtimeGroup = api.Group
type coreCandidate = api.CoreCandidate
type applyResult = api.ApplyResult

type proxySelection = api.ProxySelection
