package gw2

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"sync"
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
	index     int
	name      string
	path      string
	location  string
	character bool
}

type inventorySourceResult struct {
	index    int
	source   Source
	location string
	counts   []int64
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

	chars, err := c.Characters(ctx)
	charactersErr := err
	if charactersErr == nil {
		sort.Strings(chars)
	}
	jobs := []inventoryJob{
		{index: 0, name: "bank", path: "v2/account/bank", location: "bank"},
		{index: 1, name: "materials", path: "v2/account/materials", location: "materials"},
		{index: 2, name: "shared_inventory", path: "v2/account/inventory", location: "shared_inventory"},
	}
	if charactersErr == nil {
		for _, name := range chars {
			jobs = append(jobs, inventoryJob{
				index: len(jobs), name: "character:" + name,
				path:     "v2/characters/" + url.PathEscape(name) + "/inventory",
				location: "character:" + name + ":bags", character: true,
			})
		}
	}
	sourceResults := c.readInventorySources(ctx, jobs, indexByID, len(itemIDs))
	for i := 0; i < 3; i++ {
		appendInventorySource(&result, sourceResults[i])
	}
	if charactersErr != nil {
		result.Complete = false
		result.Sources = append(result.Sources, Source{Name: "characters", Status: "error", Error: charactersErr.Error()})
	} else {
		result.Sources = append(result.Sources, Source{Name: "characters", Status: "ok"})
		for i := 3; i < len(sourceResults); i++ {
			appendInventorySource(&result, sourceResults[i])
		}
	}
	for i := range result.Items {
		sort.Slice(result.Items[i].Locations, func(a, b int) bool {
			return result.Items[i].Locations[a].Location < result.Items[i].Locations[b].Location
		})
	}
	return result, nil
}

func (c *Client) readInventorySources(ctx context.Context, jobs []inventoryJob, indexByID map[int]int, itemCount int) []inventorySourceResult {
	work := make(chan inventoryJob)
	results := make(chan inventorySourceResult, len(jobs))
	workers := 4
	if len(jobs) < workers {
		workers = len(jobs)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range work {
				results <- c.readInventorySource(ctx, job, indexByID, itemCount)
			}
		}()
	}
	for _, job := range jobs {
		work <- job
	}
	close(work)
	ordered := make([]inventorySourceResult, len(jobs))
	for range jobs {
		result := <-results
		ordered[result.index] = result
	}
	wg.Wait()
	return ordered
}

func (c *Client) readInventorySource(ctx context.Context, job inventoryJob, indexByID map[int]int, itemCount int) inventorySourceResult {
	result := inventorySourceResult{index: job.index, location: job.location, counts: make([]int64, itemCount)}
	addSlot := func(item slot) {
		if index, ok := indexByID[item.ID]; ok && item.Count > 0 {
			result.counts[index] += item.Count
		}
	}
	if job.character {
		var bags characterBags
		if err := c.Get(ctx, job.path, &bags); err != nil {
			result.source = Source{Name: job.name, Status: "error", Error: err.Error()}
			return result
		}
		for _, bag := range bags.Bags {
			for _, item := range bag.Inventory {
				addSlot(item)
			}
		}
	} else {
		var slots []slot
		if err := c.Get(ctx, job.path, &slots); err != nil {
			result.source = Source{Name: job.name, Status: "error", Error: err.Error()}
			return result
		}
		for _, item := range slots {
			addSlot(item)
		}
	}
	result.source = Source{Name: job.name, Status: "ok"}
	return result
}

func appendInventorySource(result *FindItemsResult, source inventorySourceResult) {
	result.Sources = append(result.Sources, source.source)
	if source.source.Status == "error" {
		result.Complete = false
		return
	}
	for i, count := range source.counts {
		if count <= 0 {
			continue
		}
		result.Items[i].Locations = append(result.Items[i].Locations, Location{Location: source.location, Count: count})
		result.Items[i].ObservedTotal += count
	}
}
