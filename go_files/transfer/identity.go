package transfer

// OnIdentity is invoked when we parse an identity line from a peer.
// main.go can set this to remember hostname <-> IP mappings.
var OnIdentity func(remoteIP, hostname string)