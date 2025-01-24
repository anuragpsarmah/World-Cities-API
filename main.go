package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

type CityInfo struct {
	City *string
	ISO2 *string
}

type TrieNode struct {
	Children map[rune]*TrieNode
	Cities   []CityInfo
}

type Trie struct {
	Root *TrieNode
	mu   sync.RWMutex
}

func NewTrie() *Trie {
	return &Trie{
		Root: &TrieNode{
			Children: make(map[rune]*TrieNode),
		},
	}
}

func (t *Trie) Insert(city, iso2 *string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	lowerCity := strings.ToLower(*city)
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
				current.Cities = append(current.Cities, CityInfo{City: city, ISO2: iso2})
			}
		}
	}
}

func (t *Trie) Search(prefix *string, limit *int) []CityInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	lowerPrefix := strings.ToLower(*prefix)
	current := t.Root
	for _, char := range lowerPrefix {
		if current.Children[char] == nil {
			return []CityInfo{}
		}
		current = current.Children[char]
	}

	if len(current.Cities) > *limit {
		return current.Cities[:*limit]
	}
	return current.Cities
}

func loadCitiesData(trie *Trie, filename *string) error {
	file, err := os.Open(*filename)
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
		cityPtr := record[0]
		iso2Ptr := record[1]
		trie.Insert(&cityPtr, &iso2Ptr)
	}
	return nil
}

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
		results := trie.Search(&searchTerm, &limit)

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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	cityTrie := NewTrie()

	filename := "./world_city_data.csv"
	err := loadCitiesData(cityTrie, &filename)
	if err != nil {
		log.Fatalf("Error loading cities data: %v", err)
	}

	http.HandleFunc("/searchCities", corsMiddleware(searchCitiesHandler(cityTrie)))

	port := 3001
	log.Printf("Server starting on port %d", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}
