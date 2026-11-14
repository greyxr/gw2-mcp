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
type FailedSource struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}
type ItemLocations struct {
	ItemID    int        `json:"item_id"`
	Total     int64      `json:"total"`
	Locations []Location `json:"locations,omitempty"`
}
type FindItemsResult struct {
	Items          []ItemLocations `json:"items"`
	Complete       bool            `json:"complete"`
	SourcesChecked int             `json:"sources_checked"`
	FailedSources  []FailedSource  `json:"failed_sources,omitempty"`
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
	character bool
}

func (c *Client) FindItems(ctx context.Context, itemIDs []int) (FindItemsResult, error) {
	ids, err := UniqueIDs(itemIDs, "item_ids")
	if err != nil {
		return FindItemsResult{}, err
	}
	if c.key == "" {
		return FindItemsResult{}, fmt.Errorf("GW2 API key is required for inventory endpoints")
	}
	indexByID := make(map[int]int, len(ids))
	result := FindItemsResult{Items: make([]ItemLocations, len(ids)), Complete: true}
	for i, id := range ids {
		indexByID[id] = i
		result.Items[i].ItemID = id
	}

	chars, charactersErr := c.Characters(ctx)
	sort.Strings(chars)
	jobs := []inventoryJob{
		{name: "bank", path: "v2/account/bank"},
		{name: "materials", path: "v2/account/materials"},
		{name: "shared_inventory", path: "v2/account/inventory"},
	}
	for _, name := range chars {
		jobs = append(jobs, inventoryJob{name: "character:" + name, path: "v2/characters/" + url.PathEscape(name) + "/inventory", character: true})
	}
	slots := make([][]slot, len(jobs))
	errs := make([]error, len(jobs))
	Parallel(len(jobs), func(i int) { slots[i], errs[i] = c.inventorySlots(ctx, jobs[i]) })
	if charactersErr != nil {
		jobs, errs = append(jobs, inventoryJob{name: "characters"}), append(errs, charactersErr)
	}
	result.SourcesChecked = len(jobs)
	for i, job := range jobs {
		if errs[i] != nil {
			result.Complete = false
			result.FailedSources = append(result.FailedSources, FailedSource{Name: job.name, Error: errs[i].Error()})
			continue
		}
		counts := make([]int64, len(ids))
		for _, item := range slots[i] {
			if index, ok := indexByID[item.ID]; ok && item.Count > 0 {
				counts[index] += item.Count
			}
		}
		for j, count := range counts {
			if count > 0 {
				result.Items[j].Locations = append(result.Items[j].Locations, Location{Location: job.name, Count: count})
				result.Items[j].Total += count
			}
		}
	}
	return result, nil
}

func (c *Client) inventorySlots(ctx context.Context, job inventoryJob) ([]slot, error) {
	if !job.character {
		return getJSON[[]slot](ctx, c, job.path)
	}
	bags, err := getJSON[characterBags](ctx, c, job.path)
	var slots []slot
	for _, bag := range bags.Bags {
		slots = append(slots, bag.Inventory...)
	}
	return slots, err
}
