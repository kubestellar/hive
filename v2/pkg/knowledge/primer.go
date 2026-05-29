package knowledge

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// Primer queries wiki layers and merges results with precedence for injection
// into agent kick prompts. It runs during kick preparation — agents never talk
// to the wiki directly.
//
// After collecting BM25 results from wiki layers, the primer reranks them using
// Fisher-Rao geodesic distance on term-frequency embeddings. This catches
// semantic matches that keyword search misses.
type Primer struct {
	layers   []layerClient
	config   PrimerConfig
	logger   *slog.Logger
	embedder *EmbeddingCache
}

type layerClient struct {
	layerType LayerType
	client    *Client
}

// NewPrimer creates a primer from the configured wiki layers. Layers that are
// unreachable at construction time are skipped with a warning — the primer
// degrades gracefully.
func NewPrimer(layers []LayerConfig, config PrimerConfig, logger *slog.Logger) *Primer {
	var clients []layerClient
	for _, l := range layers {
		endpoint := l.Endpoint()
		if endpoint == "" {
			logger.Debug("skipping local-only layer (no HTTP endpoint)", "type", l.Type)
			continue
		}
		c := NewClient(endpoint, logger)
		clients = append(clients, layerClient{
			layerType: l.Type,
			client:    c,
		})
	}

	if config.MaxFacts <= 0 {
		config.MaxFacts = 25
	}

	return &Primer{
		layers:   clients,
		config:   config,
		logger:   logger,
		embedder: NewEmbeddingCache(NewTFEmbedder(EmbeddingDim)),
	}
}

// Prime queries all reachable wiki layers for facts relevant to the given
// file paths and keywords, merges with precedence, and returns a result ready
// for prompt injection.
func (p *Primer) Prime(ctx context.Context, filePaths []string, keywords []string) *PrimedKnowledge {
	start := time.Now()

	query := buildQuery(filePaths, keywords)
	if query == "" {
		return &PrimedKnowledge{}
	}

	// Query each layer; collect results tagged with their layer.
	var allFacts []Fact
	for _, lc := range p.layers {
		results, err := lc.client.Search(ctx, query, "", p.config.MaxFacts)
		if err != nil {
			p.logger.Warn("wiki layer unreachable, skipping",
				"layer", lc.layerType,
				"error", err,
			)
			continue
		}

		for _, r := range results {
			allFacts = append(allFacts, Fact{
				Slug:       r.Slug,
				Title:      r.Title,
				Type:       FactType(r.Type),
				Body:       r.Snippet,
				Confidence: r.Confidence,
				Status:     r.Status,
				Tags:       r.Tags,
				Layer:      lc.layerType,
			})
		}
	}

	merged := p.mergeWithPrecedence(allFacts)
	reranked := p.rerankFisherRao(query, merged)
	prioritized := p.applyPriority(reranked)

	if len(prioritized) > p.config.MaxFacts {
		prioritized = prioritized[:p.config.MaxFacts]
	}

	elapsed := time.Since(start).Milliseconds()
	p.logger.Info("knowledge primed",
		"facts", len(prioritized),
		"layers_queried", len(p.layers),
		"query_ms", elapsed,
	)

	return &PrimedKnowledge{
		Facts:     prioritized,
		QueryTime: elapsed,
	}
}

// rerankFisherRao reranks facts using Fisher-Rao similarity between the query
// embedding and each fact's title+body embedding. The Confidence field is updated
// to reflect the geometric score, which the downstream priority sort uses as a
// tiebreaker within the same fact type.
func (p *Primer) rerankFisherRao(query string, facts []Fact) []Fact {
	if len(facts) == 0 {
		return facts
	}

	queryEmb := p.embedder.Embed(query)
	queryGaussian := NewGaussianFromEmbedding(queryEmb, defaultSigmaInitial)

	for i := range facts {
		factText := facts[i].Title + " " + facts[i].Body
		factEmb := p.embedder.Embed(factText)
		factGaussian := NewGaussianFromEmbedding(factEmb, defaultSigmaInitial)

		frScore := GraduatedScore(queryGaussian, factGaussian, facts[i].UsageCount)

		// Blend with the original wiki confidence so BM25 signal isn't lost.
		const bm25Weight = 0.4
		facts[i].Confidence = clampScore(bm25Weight*facts[i].Confidence + (1-bm25Weight)*frScore)
	}

	sort.Slice(facts, func(i, j int) bool {
		return facts[i].Confidence > facts[j].Confidence
	})

	return facts
}

// mergeWithPrecedence deduplicates facts by slug, keeping the version from the
// highest-precedence layer (personal > project > org > community).
func (p *Primer) mergeWithPrecedence(facts []Fact) []Fact {
	seen := make(map[string]Fact)
	for _, f := range facts {
		existing, exists := seen[f.Slug]
		if !exists || f.Layer.Precedence() < existing.Layer.Precedence() {
			seen[f.Slug] = f
		}
	}

	result := make([]Fact, 0, len(seen))
	for _, f := range seen {
		result = append(result, f)
	}
	return result
}

// applyPriority sorts facts by configured type priority, then by confidence.
func (p *Primer) applyPriority(facts []Fact) []Fact {
	priorityMap := make(map[string]int)
	for i, typ := range p.config.Priority {
		priorityMap[typ] = i
	}
	defaultPriority := len(p.config.Priority)

	sort.Slice(facts, func(i, j int) bool {
		pi := defaultPriority
		pj := defaultPriority
		if idx, ok := priorityMap[string(facts[i].Type)]; ok {
			pi = idx
		}
		if idx, ok := priorityMap[string(facts[j].Type)]; ok {
			pj = idx
		}
		if pi != pj {
			return pi < pj
		}
		return facts[i].Confidence > facts[j].Confidence
	})

	return facts
}

// buildQuery constructs a search string from file paths and keywords.
func buildQuery(filePaths []string, keywords []string) string {
	var parts []string

	for _, fp := range filePaths {
		segments := strings.Split(fp, "/")
		if len(segments) > 0 {
			filename := segments[len(segments)-1]
			name := strings.TrimSuffix(filename, ".go")
			name = strings.TrimSuffix(name, ".ts")
			name = strings.TrimSuffix(name, ".tsx")
			name = strings.TrimSuffix(name, ".js")
			if name != "" && name != "index" {
				parts = append(parts, name)
			}
		}
	}

	parts = append(parts, keywords...)

	return strings.Join(parts, " ")
}
