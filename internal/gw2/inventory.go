package gw2

import (
	"context"
	"fmt"
	"net/url"
	"sort"
)

type Location struct {
	Location string `json:"location"`
	Count    int64  `json:"count"`
}
type Source struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}
type FindResult struct {
	ItemID    int        `json:"item_id"`
	Total     int64      `json:"total"`
	Locations []Location `json:"locations"`
	Sources   []Source   `json:"sources"`
	Complete  bool       `json:"complete"`
}
type ItemLocations struct {
	ItemID        int        `json:"item_id"`
	ObservedTotal int64      `json:"observed_total"`
	Locations     []Location `json:"locations"`
}
type FindItemsResult struct {
	Items    []ItemLocations `json:"items"`
	Sources  []Source        `json:"sources"`
	Complete bool            `json:"complete"`
}
type slot struct {
	ID    int   `json:"id"`
	Count int64 `json:"count"`
}
type characterBags struct {
	Bags []struct {
		Inventory []slot `json:"inventory"`
	} `json:"bags"`
}

type inventoryJob struct {
	name      string
	path      string
	location  string
	character bool
}

func (c *Client) FindItem(ctx context.Context, itemID int) (FindResult, error) {
	if itemID <= 0 {
		return FindResult{}, fmt.Errorf("item_id must be positive")
	}
	batch, err := c.FindItems(ctx, []int{itemID})
	if err != nil {
		return FindResult{}, err
	}
	item := batch.Items[0]
	return FindResult{
		ItemID: item.ItemID, Total: item.ObservedTotal, Locations: item.Locations,
		Sources: batch.Sources, Complete: batch.Complete,
	}, nil
}

func (c *Client) FindItems(ctx context.Context, itemIDs []int) (FindItemsResult, error) {
	if len(itemIDs) < 1 || len(itemIDs) > 200 {
		return FindItemsResult{}, fmt.Errorf("item_ids must contain 1 to 200 IDs")
	}
	indexByID := make(map[int]int, len(itemIDs))
	result := FindItemsResult{
		Items:    make([]ItemLocations, len(itemIDs)),
		Sources:  []Source{},
		Complete: true,
	}
	for i, id := range itemIDs {
		if id <= 0 {
			return FindItemsResult{}, fmt.Errorf("item IDs must be positive")
		}
		if _, exists := indexByID[id]; exists {
			return FindItemsResult{}, fmt.Errorf("item IDs must be unique")
		}
		indexByID[id] = i
		result.Items[i] = ItemLocations{ItemID: id, Locations: []Location{}}
	}
	if c.key == "" {
		return FindItemsResult{}, fmt.Errorf("GW2 API key is required for inventory endpoints")
	}

	chars, charactersErr := c.Characters(ctx)
	sort.Strings(chars)
	jobs := []inventoryJob{
		{name: "bank", path: "v2/account/bank", location: "bank"},
		{name: "materials", path: "v2/account/materials", location: "materials"},
		{name: "shared_inventory", path: "v2/account/inventory", location: "shared_inventory"},
		{name: "characters"},
	}
	if charactersErr == nil {
		for _, name := range chars {
			jobs = append(jobs, inventoryJob{name: "character:" + name, path: "v2/characters/" + url.PathEscape(name) + "/inventory", location: "character:" + name + ":bags", character: true})
		}
	}
	slots := make([][]slot, len(jobs))
	errors := make([]error, len(jobs))
	errors[3] = charactersErr
	parallel(len(jobs), func(i int) {
		if jobs[i].path != "" {
			slots[i], errors[i] = c.inventorySlots(ctx, jobs[i])
		}
	})
	for i, job := range jobs {
		source := Source{Name: job.name, Status: "ok"}
		if errors[i] != nil {
			source.Status, source.Error, result.Complete = "error", errors[i].Error(), false
		}
		result.Sources = append(result.Sources, source)
		if errors[i] != nil {
			continue
		}
		counts := make([]int64, len(itemIDs))
		for _, item := range slots[i] {
			if index, ok := indexByID[item.ID]; ok && item.Count > 0 {
				counts[index] += item.Count
			}
		}
		for j, count := range counts {
			if count > 0 {
				result.Items[j].Locations = append(result.Items[j].Locations, Location{Location: job.location, Count: count})
				result.Items[j].ObservedTotal += count
			}
		}
	}
	for i := range result.Items {
		sort.Slice(result.Items[i].Locations, func(a, b int) bool {
			return result.Items[i].Locations[a].Location < result.Items[i].Locations[b].Location
		})
	}
	return result, nil
}

func (c *Client) inventorySlots(ctx context.Context, job inventoryJob) ([]slot, error) {
	if !job.character {
		return getJSON[[]slot](ctx, c, job.path)
	}
	bags, err := getJSON[characterBags](ctx, c, job.path)
	var slots []slot
	if err != nil {
		return nil, err
	}
	for _, bag := range bags.Bags {
		slots = append(slots, bag.Inventory...)
	}
	return slots, err
}
