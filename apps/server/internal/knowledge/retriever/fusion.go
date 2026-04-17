package retriever

import (
	"sort"

	"agent_project/apps/server/internal/storage/postgres"
)

const reciprocalRankConstant = 60.0

type fusedChunk struct {
	chunk        postgres.ResourceChunk
	score        float64
	semanticRank int
	lexicalRank  int
}

// FuseReciprocalRank 使用 RRF 对 semantic / lexical 候选做粗融合，再交给 reranker 精排。
func FuseReciprocalRank(semantic []postgres.ResourceChunk, lexical []postgres.ResourceChunk) []postgres.ResourceChunk {
	if len(semantic) == 0 && len(lexical) == 0 {
		return []postgres.ResourceChunk{}
	}

	fused := make(map[string]*fusedChunk, len(semantic)+len(lexical))
	collectRRF(fused, semantic, true)
	collectRRF(fused, lexical, false)

	ordered := make([]fusedChunk, 0, len(fused))
	for _, candidate := range fused {
		ordered = append(ordered, *candidate)
	}

	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].score != ordered[j].score {
			return ordered[i].score > ordered[j].score
		}
		if ordered[i].semanticRank != ordered[j].semanticRank {
			return ordered[i].semanticRank < ordered[j].semanticRank
		}
		if ordered[i].lexicalRank != ordered[j].lexicalRank {
			return ordered[i].lexicalRank < ordered[j].lexicalRank
		}

		return ordered[i].chunk.ID < ordered[j].chunk.ID
	})

	result := make([]postgres.ResourceChunk, 0, len(ordered))
	for _, candidate := range ordered {
		result = append(result, candidate.chunk)
	}

	return result
}

func collectRRF(target map[string]*fusedChunk, chunks []postgres.ResourceChunk, semantic bool) {
	for rank, chunk := range chunks {
		candidate, ok := target[chunk.ID]
		if !ok {
			candidate = &fusedChunk{
				chunk:        chunk,
				semanticRank: int(^uint(0) >> 1),
				lexicalRank:  int(^uint(0) >> 1),
			}
			target[chunk.ID] = candidate
		}

		candidate.score += 1.0 / (reciprocalRankConstant + float64(rank) + 1.0)
		if semantic {
			candidate.semanticRank = rank
		} else {
			candidate.lexicalRank = rank
		}
	}
}
