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
 Governance []Change[ModelRule] `json:"governance"`
	Suppliers      []Change[Supplier]   `json:"suppliers"`
	Groups         []Change[Group]      `json:"groups"`
	Addresses      []Change[Address]    `json:"addresses"`
	GroupAddresses []GroupAddressSet    `json:"group_addresses"`
	Models         []Change[GroupModel] `json:"models"`
	Quotes         []Quote              `json:"quotes"`
}
type GroupAddressSet struct {
	GroupID string         `json:"group_id"`
	Links   []GroupAddress `json:"links"`
}
type Directory struct {
 Governance []ModelRule `json:"governance"`
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
