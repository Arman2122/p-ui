package core

import "errors"

/*
ErrKindHasNoUsers is returned for a user operation on a kind that holds none.

A typed error and not a string, because that is the whole point: the panel used
to decide whether a failed removal was worth restarting Xray for by matching
"proxy is not a UserManager" in a gRPC reply, so a reworded upstream message
would have silently restored a restart that drops every live connection on the
box. Callers ask errors.Is instead, and the call is not made at all. F314.
*/
var ErrKindHasNoUsers = errors.New("this inbound kind has no users")
