package domain

import (
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type CureStatus string

const (
	CureActive    CureStatus = "active"
	CureCompleted CureStatus = "completed"
)

type CureRecord struct {
	ID             string     `json:"id"`
	PlanID         string     `json:"plan_id"`
	SpecimenID     string     `json:"specimen_id"`
	RequestID      string     `json:"request_id"`
	StartedBy      string     `json:"started_by"`
	StartNote      string     `json:"start_note,omitempty"`
	Status         CureStatus `json:"status"`
	StartedAt      time.Time  `json:"started_at"`
	CompletedBy    string     `json:"completed_by,omitempty"`
	CompletionNote string     `json:"completion_note,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func NewCureRecord(
	id string,
	plan PrepPlan,
	requestID string,
	startedBy string,
	note string,
	now time.Time,
) (CureRecord, error) {
	if id == "" {
		return CureRecord{}, contracts.Internal("missing_generated_id", "隔离标识未生成", nil)
	}
	if plan.Status != PlanApproved && plan.Status != PlanActive {
		return CureRecord{}, contracts.Conflict("plan_not_curable", "当前方案状态不能登记固化隔离")
	}
	request, err := requiredText("请求标识", requestID, 80)
	if err != nil {
		return CureRecord{}, err
	}
	starter, err := requiredText("隔离登记人", startedBy, 80)
	if err != nil {
		return CureRecord{}, err
	}
	startNote, err := optionalText("隔离说明", note, 300)
	if err != nil {
		return CureRecord{}, err
	}
	stamp := now.UTC()
	return CureRecord{
		ID:         id,
		PlanID:     plan.ID,
		SpecimenID: plan.SpecimenID,
		RequestID:  request,
		StartedBy:  starter,
		StartNote:  startNote,
		Status:     CureActive,
		StartedAt:  stamp,
		UpdatedAt:  stamp,
	}, nil
}

func (c CureRecord) IsActive() bool {
	return c.Status == CureActive
}

func (c *CureRecord) Complete(
	requestID string,
	completedBy string,
	note string,
	now time.Time,
) error {
	if _, err := requiredText("请求标识", requestID, 80); err != nil {
		return err
	}
	if c.Status != CureActive {
		return contracts.Conflict("cure_not_completable", "只有未结束的固化隔离可以确认解除")
	}
	completer, err := requiredText("解除确认人", completedBy, 80)
	if err != nil {
		return err
	}
	completionNote, err := optionalText("解除说明", note, 300)
	if err != nil {
		return err
	}
	stamp := now.UTC()
	c.Status = CureCompleted
	c.CompletedBy = completer
	c.CompletionNote = completionNote
	c.CompletedAt = &stamp
	c.UpdatedAt = stamp
	return nil
}
