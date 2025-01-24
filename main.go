package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// CityInfo represents city data
type CityInfo struct {
	City *string
	ISO2 *string
}

// TrieNode represents a node in the trie
type TrieNode struct {
	Children map[rune]*TrieNode
	Cities   []CityInfo
}

// Trie represents a prefix tree for city searching
type Trie struct {
	Root *TrieNode
	mu   sync.RWMutex
}

// RateLimiter manages rate limiting per IP
type RateLimiter struct {
	visitors map[string]*rate.Limiter
	mu       sync.Mutex
}

// NewRateLimiter initializes a rate limiter with periodic cleanup
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*rate.Limiter),
	}

	// Cleanup unused IPs every 10 minutes
	go func() {
		for {
			time.Sleep(10 * time.Minute)
			rl.cleanupOldIPs()
		}
	}()

	return rl
}

// GetLimiter returns the rate limiter for a given IP, creating one if necessary
func (rl *RateLimiter) GetLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	limiter, exists := rl.visitors[ip]
	if !exists {
		limiter = rate.NewLimiter(rate.Limit(5), 10) // 5 requests per second, burst of 10
		rl.visitors[ip] = limiter
	}
	return limiter
}

// Cleanup function to remove inactive IPs
func (rl *RateLimiter) cleanupOldIPs() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	for ip, limiter := range rl.visitors {
		if limiter.AllowN(time.Now(), 1) { // If unused for a while, remove it
			delete(rl.visitors, ip)
		}
	}
}

// NewTrie initializes a new trie
func NewTrie() *Trie {
	return &Trie{
		Root: &TrieNode{
			Children: make(map[rune]*TrieNode),
		},
	}
}

// Insert adds a city to the trie
func (t *Trie) Insert(city, iso2 string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	lowerCity := strings.ToLower(city)
	for i := 0; i < len(lowerCity); i++ {
		currentWord := lowerCity[i:]
		current := t.Root
		for _, char := range currentWord {
			if current.Children[char] == nil {
				current.Children[char] = &TrieNode{
					Children: make(map[rune]*TrieNode),
				}
			}
			current = current.Children[char]
			if len(current.Cities) < 1000 {
				current.Cities = append(current.Cities, CityInfo{City: &city, ISO2: &iso2})
			}
		}
	}
}

// Search returns city matches for a given prefix
func (t *Trie) Search(prefix string, limit int) []CityInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	lowerPrefix := strings.ToLower(prefix)
	current := t.Root
	for _, char := range lowerPrefix {
		if current.Children[char] == nil {
			return []CityInfo{}
		}
		current = current.Children[char]
	}
	if len(current.Cities) > limit {
		return current.Cities[:limit]
	}
	return current.Cities
}

// Load cities data from CSV into the trie
func loadCitiesData(trie *Trie, filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return err
	}

	for _, record := range records[1:] {
		city := record[0]
		iso2 := record[1]
		trie.Insert(city, iso2)
	}
	return nil
}

// Middleware to handle CORS
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	}
}

// Extract client IP, supporting proxies
func getClientIP(r *http.Request) string {
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
	}
	return ip
}

// Middleware for rate limiting
func rateLimitMiddleware(rateLimiter *RateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)

		// Get rate limiter for this IP
		limiter := rateLimiter.GetLimiter(ip)

		// Check if the request is allowed
		if !limiter.Allow() {
			http.Error(w, "Rate limit exceeded. Please slow down.", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	}
}

// Handler for city search
func searchCitiesHandler(trie *Trie) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		searchTerm := r.URL.Query().Get("q")
		limitStr := r.URL.Query().Get("limit")

		if len(searchTerm) < 2 {
			http.Error(w, "Search term too short", http.StatusBadRequest)
			return
		}

		limit := 10
		var err error
		if limitStr != "" {
			limit, err = strconv.Atoi(limitStr)
			if err != nil || limit < 1 {
				http.Error(w, "Invalid limit", http.StatusBadRequest)
				return
			}
		}

		limit = min(limit, 1000)
		results := trie.Search(searchTerm, limit)

		response := struct {
			FilteredResults []string `json:"filteredResults"`
			Count           int      `json:"count"`
			Limit           int      `json:"limit"`
		}{
			Count: len(results),
			Limit: limit,
		}

		for _, city := range results {
			response.FilteredResults = append(response.FilteredResults,
				fmt.Sprintf("%s, %s", *city.City, *city.ISO2))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// Min function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	cityTrie := NewTrie()
	filename := "./world_city_data.csv"
	err := loadCitiesData(cityTrie, filename)
	if err != nil {
		log.Fatalf("Error loading cities data: %v", err)
	}

	// Create a rate limiter
	rateLimiter := NewRateLimiter()

	// Register routes with middleware
	http.HandleFunc("/searchCities",
		corsMiddleware(
			rateLimitMiddleware(rateLimiter,
				searchCitiesHandler(cityTrie),
			),
		),
	)

	port := 3001
	log.Printf("Server starting on port %d", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}
