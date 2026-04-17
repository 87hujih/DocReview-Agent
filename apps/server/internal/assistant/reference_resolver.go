package assistant

import "strings"

// ResolvedReference 表示当前用户问题被解析到的目标 section。
type ResolvedReference struct {
	SectionID   string
	SectionType string
	EntityName  string
	Reason      string
}

// ReferenceResolver 负责把承接式引用解析成稳定 section 落点。
type ReferenceResolver struct{}

// Resolve 按显式实体、ordinal、anaphora 的顺序解析当前问题。
func (r ReferenceResolver) Resolve(query string, snapshot *SessionContextSnapshot) *ResolvedReference {
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" || snapshot == nil {
		return nil
	}

	if resolved := resolveExplicitEntity(trimmedQuery, snapshot); resolved != nil {
		return resolved
	}
	if resolved := resolveOrdinalReference(trimmedQuery, snapshot); resolved != nil {
		return resolved
	}
	if resolved := resolveAnaphora(trimmedQuery, snapshot); resolved != nil {
		return resolved
	}

	return nil
}

func resolveExplicitEntity(query string, snapshot *SessionContextSnapshot) *ResolvedReference {
	for _, entity := range snapshot.LastEnumeratedEntities {
		if strings.TrimSpace(entity.EntityName) == "" {
			continue
		}
		if strings.Contains(query, entity.EntityName) {
			return &ResolvedReference{
				SectionID:   entity.SectionID,
				SectionType: entity.SectionType,
				EntityName:  entity.EntityName,
				Reason:      "explicit_entity",
			}
		}
	}

	for _, reference := range snapshot.OrdinalReferenceFrame {
		if strings.TrimSpace(reference.EntityName) == "" {
			continue
		}
		if strings.Contains(query, reference.EntityName) {
			return &ResolvedReference{
				SectionID:   reference.SectionID,
				SectionType: reference.SectionType,
				EntityName:  reference.EntityName,
				Reason:      "explicit_entity",
			}
		}
	}

	return nil
}

func resolveOrdinalReference(query string, snapshot *SessionContextSnapshot) *ResolvedReference {
	ordinal := extractOrdinal(query)
	if ordinal == 0 {
		return nil
	}

	for _, reference := range snapshot.OrdinalReferenceFrame {
		if reference.Ordinal != ordinal {
			continue
		}
		return &ResolvedReference{
			SectionID:   reference.SectionID,
			SectionType: reference.SectionType,
			EntityName:  reference.EntityName,
			Reason:      "ordinal_reference",
		}
	}

	return nil
}

func resolveAnaphora(query string, snapshot *SessionContextSnapshot) *ResolvedReference {
	if snapshot.ActiveSection == nil {
		return nil
	}
	if !containsAnaphora(query) {
		return nil
	}

	entityName := ""
	if snapshot.ActiveEntityName != nil {
		entityName = *snapshot.ActiveEntityName
	}

	return &ResolvedReference{
		SectionID:   snapshot.ActiveSection.ID,
		SectionType: snapshot.ActiveSection.Type,
		EntityName:  entityName,
		Reason:      "anaphora",
	}
}

func extractOrdinal(query string) int {
	switch {
	case strings.Contains(query, "第一个"), strings.Contains(query, "第1个"):
		return 1
	case strings.Contains(query, "第二个"), strings.Contains(query, "第2个"):
		return 2
	case strings.Contains(query, "第三个"), strings.Contains(query, "第3个"):
		return 3
	default:
		return 0
	}
}

func containsAnaphora(query string) bool {
	for _, token := range []string{"那个项目", "这个项目", "上面那个项目", "那个经历", "这个经历", "上面那个经历"} {
		if strings.Contains(query, token) {
			return true
		}
	}

	return false
}
