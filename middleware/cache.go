package middleware

import (
	"container/list"
	"sync"

	"github.com/KARTIKrocks/sqlguard/analyzer"
)

// analysisCache memoizes analyzer.Analyze results so each distinct query is
// parsed and rule-checked once instead of on every execution. It is a bounded
// LRU keyed on the **exact** query string.
//
// Why the exact string and not the fingerprint: the fingerprint folds away
// literal values, but a few rules read literal-derived facts the fingerprint
// discards — large-offset (OffsetValue), in-list-too-large (MaxInListLen), and
// leading-wildcard min-length (LeadingWildcardTermLen). Two queries can share a
// fingerprint yet warrant different findings, so fingerprint-keying would cache
// a wrong verdict. Identical query strings always analyze identically, which
// makes the exact string the only fully-correct key — and an effective one,
// since parameterized queries (the common case) and repeated identical queries
// hit while varying-literal queries miss (and those need re-analysis anyway).
//
// Cached result slices are shared and must be treated as read-only by callers
// (Guard.report and the reporters do). An analysisCache is safe for concurrent
// use.
type analysisCache struct {
	mu       sync.Mutex
	ll       *list.List
	items    map[string]*list.Element
	capacity int
}

type cacheEntry struct {
	key     string
	results []analyzer.Result
}

func newAnalysisCache(capacity int) *analysisCache {
	return &analysisCache{
		ll:       list.New(),
		items:    make(map[string]*list.Element),
		capacity: capacity,
	}
}

// get returns the cached results for query and true, or nil and false on a
// miss. A cached empty/nil slice is a hit (the query was analyzed and produced
// no findings) — exactly the common case worth memoizing.
func (c *analysisCache) get(query string) ([]analyzer.Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[query]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*cacheEntry).results, true
	}
	return nil, false
}

// put stores results for query, evicting the least-recently-used entry when the
// cache exceeds its capacity.
func (c *analysisCache) put(query string, results []analyzer.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[query]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*cacheEntry).results = results
		return
	}
	el := c.ll.PushFront(&cacheEntry{key: query, results: results})
	c.items[query] = el
	if c.ll.Len() > c.capacity {
		if oldest := c.ll.Back(); oldest != nil {
			c.ll.Remove(oldest)
			delete(c.items, oldest.Value.(*cacheEntry).key)
		}
	}
}

func (c *analysisCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
