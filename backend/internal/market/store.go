package market

import "context"

type Change[T any] struct {
	Value        T     `json:"value"`
	BaseRevision int64 `json:"base_revision"`
}

// Changes is the already-authorized publication input. F2 performs contribution
// ACL/report/approval checks in the same transaction as Apply. Nothing here is a
// public admin bypass. Stable group-address IDs survive reordering.
type Changes struct {
	Suppliers      []Change[Supplier]
	Groups         []Change[Group]
	Addresses      []Change[Address]
	GroupAddresses []GroupAddressSet
	Models         []Change[GroupModel]
	Quotes         []Quote
}
type GroupAddressSet struct {
	GroupID string         `json:"group_id"`
	Links   []GroupAddress `json:"links"`
}
type Directory struct {
	Suppliers      []Supplier     `json:"suppliers"`
	Groups         []Group        `json:"groups"`
	Addresses      []Address      `json:"addresses"`
	GroupAddresses []GroupAddress `json:"group_addresses"`
	Models         []GroupModel   `json:"models"`
	Quotes         []Quote        `json:"quotes"`
}
type Store interface {
	Snapshot(context.Context) (Directory, error)
	Apply(context.Context, string, Changes) error
}
