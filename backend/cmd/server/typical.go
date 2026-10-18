package main

import (
	"context"
	"sort"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/typical"
)

type typicalSource struct {
	runtime *groupchannel.Runtime
	values  func(context.Context) (int64, int64, error)
}

func (s typicalSource) Current(ctx context.Context) (typical.Input, error) {
	state, err := s.runtime.Current()
	if err != nil {
		return typical.Input{}, err
	}
	fx, fee, err := s.values(ctx)
	if err != nil {
		return typical.Input{}, err
	}
	in := typical.Input{Directory: state.Directory, USDCNYNano: fx, FeeRateNano: fee, Channels: []typical.Channel{}}
	active := map[string]bool{}
	for _, r := range state.Data.Channels {
		active[r.ID] = r.OwnerActive && r.Status == channel.StatusListed
	}
	for _, v := range state.Views {
		if !active[v.ID] {
			continue
		}
		for _, m := range v.Models {
			if !m.Enabled || m.Excluded {
				continue
			}
			discount, err := groupchannel.ParseDiscount(m.Discount)
			if err != nil {
				return typical.Input{}, err
			}
			in.Channels = append(in.Channels, typical.Channel{ID: v.ID, GroupID: v.GroupID, ModelID: m.ModelID, DiscountNano: discount, Name: v.Name})
		}
	}
	sort.Slice(in.Channels, func(i, j int) bool {
		return in.Channels[i].ID+"\x00"+in.Channels[i].ModelID < in.Channels[j].ID+"\x00"+in.Channels[j].ModelID
	})
	return in, nil
}
