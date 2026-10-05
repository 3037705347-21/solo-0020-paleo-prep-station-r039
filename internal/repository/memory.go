package repository

import (
	"sort"
	"sync"

	"example.com/paleo-prep-station/internal/domain"
)

type state struct {
	specimens    map[string]domain.Specimen
	plans        map[string]domain.PrepPlan
	progress     map[string][]domain.ProgressEntry
	releases     map[string]domain.ReleaseRecord
	cures        map[string][]domain.CureRecord
	cureRequests map[string]string
}

type MemoryRepository struct {
	mu    sync.Mutex
	state state
}

func NewMemory() *MemoryRepository {
	return &MemoryRepository{
		state: state{
			specimens:    make(map[string]domain.Specimen),
			plans:        make(map[string]domain.PrepPlan),
			progress:     make(map[string][]domain.ProgressEntry),
			releases:     make(map[string]domain.ReleaseRecord),
			cures:        make(map[string][]domain.CureRecord),
			cureRequests: make(map[string]string),
		},
	}
}

func (r *MemoryRepository) Read(fn func(*Mutation) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fn(cloneMutation(r.state))
}

func (r *MemoryRepository) Mutate(fn func(*Mutation) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tx := cloneMutation(r.state)
	if err := fn(tx); err != nil {
		return err
	}
	r.state = tx.snapshot()
	return nil
}

func cloneMutation(source state) *Mutation {
	copied := state{
		specimens:    make(map[string]domain.Specimen, len(source.specimens)),
		plans:        make(map[string]domain.PrepPlan, len(source.plans)),
		progress:     make(map[string][]domain.ProgressEntry, len(source.progress)),
		releases:     make(map[string]domain.ReleaseRecord, len(source.releases)),
		cures:        make(map[string][]domain.CureRecord, len(source.cures)),
		cureRequests: make(map[string]string, len(source.cureRequests)),
	}
	for key, value := range source.specimens {
		copied.specimens[key] = value
	}
	for key, value := range source.plans {
		copied.plans[key] = value
	}
	for key, values := range source.progress {
		copied.progress[key] = append([]domain.ProgressEntry(nil), values...)
	}
	for key, value := range source.releases {
		copied.releases[key] = value
	}
	for key, values := range source.cures {
		copied.cures[key] = append([]domain.CureRecord(nil), values...)
	}
	for key, value := range source.cureRequests {
		copied.cureRequests[key] = value
	}
	return &Mutation{state: copied}
}

type Mutation struct {
	state state
}

func (m *Mutation) snapshot() state {
	return m.state
}

func (m *Mutation) GetSpecimen(id string) (domain.Specimen, bool) {
	value, ok := m.state.specimens[id]
	return value, ok
}

func (m *Mutation) FindSpecimenByCatalog(catalogNumber string) (domain.Specimen, bool) {
	for _, value := range m.state.specimens {
		if value.CatalogNumber == catalogNumber {
			return value, true
		}
	}
	return domain.Specimen{}, false
}

func (m *Mutation) ListSpecimens() []domain.Specimen {
	values := make([]domain.Specimen, 0, len(m.state.specimens))
	for _, value := range m.state.specimens {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
	return values
}

func (m *Mutation) PutSpecimen(value domain.Specimen) {
	m.state.specimens[value.ID] = value
}

func (m *Mutation) GetPlan(id string) (domain.PrepPlan, bool) {
	value, ok := m.state.plans[id]
	return value, ok
}

func (m *Mutation) ListPlansForSpecimen(specimenID string) []domain.PrepPlan {
	values := make([]domain.PrepPlan, 0)
	for _, value := range m.state.plans {
		if value.SpecimenID == specimenID {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
	return values
}

func (m *Mutation) PutPlan(value domain.PrepPlan) {
	m.state.plans[value.ID] = value
}

func (m *Mutation) HasOpenPlan(specimenID string) bool {
	for _, value := range m.state.plans {
		if value.SpecimenID == specimenID && value.IsOpen() {
			return true
		}
	}
	return false
}

func (m *Mutation) ProgressForPlan(planID string) []domain.ProgressEntry {
	return append([]domain.ProgressEntry(nil), m.state.progress[planID]...)
}

func (m *Mutation) AppendProgress(value domain.ProgressEntry) {
	m.state.progress[value.PlanID] = append(m.state.progress[value.PlanID], value)
}

func (m *Mutation) ReleaseForSpecimen(specimenID string) (domain.ReleaseRecord, bool) {
	for _, value := range m.state.releases {
		if value.SpecimenID == specimenID {
			return value, true
		}
	}
	return domain.ReleaseRecord{}, false
}

func (m *Mutation) PutRelease(value domain.ReleaseRecord) {
	m.state.releases[value.ID] = value
}

// ListCures returns the full cure history for a plan, including the currently
// active isolation, ordered from the oldest registration to the newest.
func (m *Mutation) ListCures(planID string) []domain.CureRecord {
	values := append([]domain.CureRecord(nil), m.state.cures[planID]...)
	sort.Slice(values, func(i, j int) bool {
		if values[i].StartedAt.Equal(values[j].StartedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].StartedAt.Before(values[j].StartedAt)
	})
	return values
}

func (m *Mutation) FindCure(planID string, cureID string) (domain.CureRecord, bool) {
	for _, value := range m.state.cures[planID] {
		if value.ID == cureID {
			return value, true
		}
	}
	return domain.CureRecord{}, false
}

// FindActiveCure returns the single unfinished isolation of a plan, if any.
func (m *Mutation) FindActiveCure(planID string) (domain.CureRecord, bool) {
	for _, value := range m.state.cures[planID] {
		if value.IsActive() {
			return value, true
		}
	}
	return domain.CureRecord{}, false
}

func (m *Mutation) FindCureRequest(
	planID string,
	operation string,
	requestID string,
) (domain.CureRecord, bool) {
	cureID, ok := m.state.cureRequests[cureRequestKey(planID, operation, requestID)]
	if !ok {
		return domain.CureRecord{}, false
	}
	return m.FindCure(planID, cureID)
}

func (m *Mutation) AppendCure(value domain.CureRecord) {
	m.state.cures[value.PlanID] = append(m.state.cures[value.PlanID], value)
}

// ReplaceCure updates the stored record with the same plan and cure ID in
// place, keeping the history a single row per isolation. It returns false
// when no matching record exists.
func (m *Mutation) ReplaceCure(value domain.CureRecord) bool {
	values := m.state.cures[value.PlanID]
	for i := range values {
		if values[i].ID == value.ID {
			values[i] = value
			return true
		}
	}
	return false
}

func (m *Mutation) RememberCureRequest(
	planID string,
	operation string,
	requestID string,
	cureID string,
) {
	m.state.cureRequests[cureRequestKey(planID, operation, requestID)] = cureID
}

func cureRequestKey(planID string, operation string, requestID string) string {
	return operation + ":" + planID + ":" + requestID
}
