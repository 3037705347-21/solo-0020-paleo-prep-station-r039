package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func (s *Service) CreatePlan(
	ctx context.Context,
	specimenID string,
	request contracts.CreatePlanRequest,
) (domain.PrepPlan, error) {
	if err := ctx.Err(); err != nil {
		return domain.PrepPlan{}, err
	}

	var created domain.PrepPlan
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		specimen, ok := m.GetSpecimen(specimenID)
		if !ok {
			return notFoundSpecimen(specimenID)
		}
		if m.HasOpenPlan(specimenID) {
			return contracts.Conflict("open_plan_exists", "该标本已存在未结束的修复方案")
		}
		plan, err := domain.NewPrepPlan(
			s.nextID("plan"),
			specimen,
			request.Objective,
			request.Method,
			request.Adhesive,
			request.TargetExposure,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := specimen.MarkPlanned(s.now()); err != nil {
			return err
		}
		m.PutPlan(plan)
		m.PutSpecimen(specimen)
		created = plan
		return nil
	})
	return created, err
}

func (s *Service) ApprovePlan(
	ctx context.Context,
	planID string,
	request contracts.ApprovalRequest,
) (domain.PrepPlan, error) {
	if err := ctx.Err(); err != nil {
		return domain.PrepPlan{}, err
	}

	var approved domain.PrepPlan
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		specimen, ok := m.GetSpecimen(plan.SpecimenID)
		if !ok {
			return notFoundSpecimen(plan.SpecimenID)
		}
		if err := plan.Approve(request.ApprovedBy, s.now()); err != nil {
			return err
		}
		if err := specimen.MarkPreparing(s.now()); err != nil {
			return err
		}
		m.PutPlan(plan)
		m.PutSpecimen(specimen)
		approved = plan
		return nil
	})
	return approved, err
}

func (s *Service) GetPlan(ctx context.Context, planID string) (PlanSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return PlanSnapshot{}, err
	}
	var snapshot PlanSnapshot
	err := s.repo.Read(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		snapshot = PlanSnapshot{
			Plan:     plan,
			Progress: m.ProgressForPlan(planID),
			Cures:    m.ListCures(planID),
		}
		return nil
	})
	return snapshot, err
}

// CureResult is the outcome of a start-cure request. Replayed is true when the
// request identifier was already applied, in which case Record is the single
// cure created by the first arrival and no new isolation is registered.
type CureResult struct {
	Record   domain.CureRecord
	Replayed bool
}

func (s *Service) StartCure(
	ctx context.Context,
	planID string,
	request contracts.StartCureRequest,
) (CureResult, error) {
	if err := ctx.Err(); err != nil {
		return CureResult{}, err
	}

	var result CureResult
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		requestID, err := domain.ValidateCureRequestID(request.RequestID)
		if err != nil {
			return err
		}
		// A repeated registration request always resolves to the cure created
		// by its first arrival, regardless of which isolation round is open.
		if existing, found := m.FindCureRequest(planID, "start", requestID); found {
			result = CureResult{Record: existing, Replayed: true}
			return nil
		}
		if active, found := m.FindActiveCure(planID); found {
			return contracts.Conflict(
				"cure_active",
				"该方案已存在未结束的固化隔离："+active.ID,
			)
		}
		record, err := domain.NewCureRecord(
			s.nextID("cure"),
			plan,
			requestID,
			request.StartedBy,
			request.Note,
			s.now(),
		)
		if err != nil {
			return err
		}
		if plan.Status == domain.PlanApproved {
			if err := plan.Begin(s.now()); err != nil {
				return err
			}
		}
		m.AppendCure(record)
		m.RememberCureRequest(planID, "start", requestID, record.ID)
		m.PutPlan(plan)
		result = CureResult{Record: record}
		return nil
	})
	return result, err
}

func (s *Service) CompleteCure(
	ctx context.Context,
	planID string,
	cureID string,
	request contracts.CompleteCureRequest,
) (CureResult, error) {
	if err := ctx.Err(); err != nil {
		return CureResult{}, err
	}

	var result CureResult
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		if _, ok := m.GetPlan(planID); !ok {
			return notFoundPlan(planID)
		}
		requestID, err := domain.ValidateCureRequestID(request.RequestID)
		if err != nil {
			return err
		}
		// A repeated completion request resolves to the same cure and result,
		// so a retried or replayed request can never close a later isolation.
		if existing, found := m.FindCureRequest(planID, "complete", requestID); found {
			if existing.ID != cureID {
				return contracts.Conflict(
					"cure_request_mismatch",
					"该解除请求已用于另一条固化隔离",
				)
			}
			result = CureResult{Record: existing, Replayed: true}
			return nil
		}
		record, found := m.FindCure(planID, cureID)
		if !found {
			return contracts.NotFound("cure_not_found", "未找到固化隔离 "+cureID)
		}
		if err := record.Complete(request.CompletedBy, request.Note, s.now()); err != nil {
			return err
		}
		if !m.ReplaceCure(record) {
			return contracts.NotFound("cure_not_found", "未找到固化隔离 "+cureID)
		}
		m.RememberCureRequest(planID, "complete", requestID, cureID)
		result = CureResult{Record: record}
		return nil
	})
	return result, err
}

func (s *Service) ListPlansForSpecimen(
	ctx context.Context,
	specimenID string,
) ([]domain.PrepPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var values []domain.PrepPlan
	err := s.repo.Read(func(m *repository.Mutation) error {
		if _, ok := m.GetSpecimen(specimenID); !ok {
			return notFoundSpecimen(specimenID)
		}
		values = m.ListPlansForSpecimen(specimenID)
		return nil
	})
	return values, err
}
