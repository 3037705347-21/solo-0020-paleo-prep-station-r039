package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/httpapi"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/station"
)

type specimenView struct {
	ID            string `json:"id"`
	CatalogNumber string `json:"catalog_number"`
	Status        string `json:"status"`
}

type planView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
	Status     string `json:"status"`
}

type planSnapshot struct {
	Plan     planView `json:"plan"`
	Progress []struct {
		Percent int `json:"percent"`
	} `json:"progress"`
}

type releaseView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
}

type cureView struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	RequestID   string `json:"request_id"`
	StartedBy   string `json:"started_by"`
	CompletedBy string `json:"completed_by"`
}

type cureSnapshot struct {
	Plan     planView `json:"plan"`
	Progress []struct {
		Percent int `json:"percent"`
	} `json:"progress"`
	Cures []cureView `json:"cures"`
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to check")
	flag.Parse()

	if err := run(*workflow); err != nil {
		fmt.Fprintln(os.Stderr, "workflow check failed:", err)
		os.Exit(1)
	}
	fmt.Printf("workflow check passed: %s\n", *workflow)
}

func run(workflow string) error {
	handler := httpapi.NewRouter(station.New(repository.NewMemory()))
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	switch workflow {
	case "register-specimen":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		if specimen.Status != "registered" {
			return fmt.Errorf("expected registered status, got %s", specimen.Status)
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.ID != specimen.ID {
			return errors.New("registered specimen cannot be read back")
		}
		return nil

	case "approve-prep-plan":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		plan, err := createPlan(client, server.URL, specimen.ID)
		if err != nil {
			return err
		}
		var approved planView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/plans/"+plan.ID+"/approval",
			contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
			http.StatusOK,
			&approved,
		); err != nil {
			return err
		}
		if approved.Status != "approved" {
			return fmt.Errorf("expected approved plan, got %s", approved.Status)
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "preparing" {
			return fmt.Errorf("expected preparing specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "log-preparation":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 35, "矩阵剥离"); err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var snapshot planSnapshot
		if err := requestJSON(client, http.MethodGet, server.URL+"/plans/"+plan.ID, nil, http.StatusOK, &snapshot); err != nil {
			return err
		}
		if snapshot.Plan.Status != "completed" || len(snapshot.Progress) != 2 {
			return errors.New("plan did not complete with exactly two progress entries")
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "stabilized" {
			return fmt.Errorf("expected stabilized specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "release-for-study":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var release releaseView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/specimens/"+specimen.ID+"/release",
			contracts.ReleaseRequest{
				Purpose:    "比较解剖研究",
				Recipient:  "地层研究组",
				ApprovedBy: "keeper-zhou",
			},
			http.StatusCreated,
			&release,
		); err != nil {
			return err
		}
		if release.SpecimenID != specimen.ID {
			return errors.New("release record references the wrong specimen")
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.Status != "available" {
			return fmt.Errorf("expected available specimen, got %s", fetched.Status)
		}
		return nil

	case "cure-isolation":
		return checkCureIsolation(client, server.URL)
	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

func checkCureIsolation(client *http.Client, baseURL string) error {
	specimen, plan, err := completedPlanSetup(client, baseURL)
	if err != nil {
		return err
	}
	planURL := baseURL + "/plans/" + plan.ID

	// One progress entry before isolation establishes history that must stay
	// untouched while the cure is active and complete afterwards.
	if err := recordProgress(client, baseURL, plan.ID, 40, "基质去除"); err != nil {
		return err
	}

	// First registration creates exactly one active isolation and moves the
	// plan to active.
	cure, status, err := startCure(client, planURL, contracts.StartCureRequest{
		RequestID: "start-request-001",
		StartedBy: "leader-wang",
		Note:      "表面加固后密闭固化",
	}, http.StatusCreated)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("expected first start to return 201, got %d", status)
	}
	if cure.Status != "active" {
		return fmt.Errorf("expected active cure, got %s", cure.Status)
	}

	// The plan page must show the in-progress isolation immediately.
	activeSnapshot, err := fetchCureSnapshot(client, planURL)
	if err != nil {
		return err
	}
	if activeSnapshot.Plan.Status != "active" || len(activeSnapshot.Cures) != 1 {
		return errors.New("registered isolation is not visible on the plan page")
	}
	if activeSnapshot.Cures[0].ID != cure.ID || activeSnapshot.Cures[0].Status != "active" {
		return errors.New("plan page shows an inconsistent active isolation")
	}

	// Progress must be explicitly rejected during isolation, and neither the
	// plan nor its history may change on failure.
	if err := expectStatus(client, http.MethodPost, planURL+"/progress", contracts.ProgressRequest{
		Stage:      "隔离期偷记",
		Percent:    55,
		Note:       "应当被拒绝",
		RecordedBy: "preparator-chen",
	}, http.StatusConflict); err != nil {
		return err
	}
	blockedSnapshot, err := fetchCureSnapshot(client, planURL)
	if err != nil {
		return err
	}
	if len(blockedSnapshot.Progress) != 1 || blockedSnapshot.Progress[0].Percent != 40 {
		return errors.New("rejected progress during isolation changed plan history")
	}
	if len(blockedSnapshot.Cures) != 1 || blockedSnapshot.Cures[0].Status != "active" {
		return errors.New("rejected progress changed isolation state")
	}

	// A second, distinct registration request must fail while one is active.
	if err := expectStatus(client, http.MethodPost, planURL+"/cures", contracts.StartCureRequest{
		RequestID: "start-request-002",
		StartedBy: "leader-wang",
	}, http.StatusConflict); err != nil {
		return err
	}
	afterReject, err := fetchCureSnapshot(client, planURL)
	if err != nil {
		return err
	}
	if len(afterReject.Cures) != 1 || afterReject.Cures[0].ID != cure.ID {
		return errors.New("conflicting registration left a second or dangling isolation")
	}

	// Replaying the original registration is idempotent: same cure, 200, no
	// extra row.
	replay, status, err := startCure(client, planURL, contracts.StartCureRequest{
		RequestID: "start-request-001",
		StartedBy: "leader-wang",
		Note:      "重复到达的登记请求",
	}, http.StatusOK)
	if err != nil {
		return err
	}
	if replay.ID != cure.ID {
		return errors.New("replayed registration created a different isolation")
	}
	replaySnapshot, err := fetchCureSnapshot(client, planURL)
	if err != nil {
		return err
	}
	if len(replaySnapshot.Cures) != 1 || replaySnapshot.Cures[0].ID != cure.ID {
		return errors.New("replayed registration left a duplicate isolation")
	}

	// Completion by another person closes the same cure row in place.
	if _, err := completeCure(client, planURL, cure.ID, contracts.CompleteCureRequest{
		RequestID:   "complete-request-001",
		CompletedBy: "officer-zhao",
		Note:        "固化完成确认解除",
	}); err != nil {
		return err
	}
	closedSnapshot, err := fetchCureSnapshot(client, planURL)
	if err != nil {
		return err
	}
	if len(closedSnapshot.Cures) != 1 {
		return errors.New("completion appended a duplicate row instead of closing the cure")
	}
	if closedSnapshot.Cures[0].ID != cure.ID || closedSnapshot.Cures[0].Status != "completed" {
		return errors.New("plan page still shows an unfinished isolation after completion")
	}

	// Replaying the completion returns the same completed cure without changes.
	completionReplay, err := completeCure(client, planURL, cure.ID, contracts.CompleteCureRequest{
		RequestID:   "complete-request-001",
		CompletedBy: "officer-zhao",
	})
	if err != nil {
		return err
	}
	if completionReplay.ID != cure.ID || completionReplay.Status != "completed" {
		return errors.New("replayed completion did not return the same closed cure")
	}

	// Progress recording resumes; the plan stays active for a second round.
	if err := recordProgress(client, baseURL, plan.ID, 60, "继续去除基质"); err != nil {
		return err
	}

	// Invalid input is rejected without consuming any state.
	if err := expectStatus(client, http.MethodPost, planURL+"/cures", contracts.StartCureRequest{
		RequestID: "",
		StartedBy: "leader-wang",
	}, http.StatusBadRequest); err != nil {
		return err
	}

	// A second isolation round starts with a fresh request id.
	second, status, err := startCure(client, planURL, contracts.StartCureRequest{
		RequestID: "start-request-002",
		StartedBy: "leader-wang",
		Note:      "第二轮固化",
	}, http.StatusCreated)
	if err != nil {
		return err
	}
	if second.ID == cure.ID || second.Status != "active" {
		return errors.New("second isolation round did not start as a new active cure")
	}

	// The previous round's successful start request, arriving again, must
	// resolve to the old cure and never touch the new round.
	staleStart, status, err := startCure(client, planURL, contracts.StartCureRequest{
		RequestID: "start-request-001",
		StartedBy: "leader-wang",
	}, http.StatusOK)
	if err != nil {
		return err
	}
	if staleStart.ID != cure.ID || staleStart.Status != "completed" {
		return errors.New("stale start request was carried into the new isolation round")
	}
	secondSnapshot, err := fetchCureSnapshot(client, planURL)
	if err != nil {
		return err
	}
	if len(secondSnapshot.Cures) != 2 || secondSnapshot.Cures[1].ID != second.ID ||
		secondSnapshot.Cures[1].Status != "active" {
		return errors.New("stale start replay corrupted the second isolation round")
	}

	// The previous completion request cannot close the new round, neither with
	// a mismatching replayed id nor an invalid id.
	if err := expectStatus(
		client,
		http.MethodPost,
		planURL+"/cures/"+second.ID+"/completion",
		contracts.CompleteCureRequest{RequestID: "complete-request-001", CompletedBy: "officer-zhao"},
		http.StatusConflict,
	); err != nil {
		return err
	}
	if err := expectStatus(
		client,
		http.MethodPost,
		planURL+"/cures/"+second.ID+"/completion",
		contracts.CompleteCureRequest{RequestID: "   ", CompletedBy: "officer-zhao"},
		http.StatusBadRequest,
	); err != nil {
		return err
	}
	if stillActive, err := fetchCureSnapshot(client, planURL); err != nil ||
		len(stillActive.Cures) != 2 || stillActive.Cures[1].Status != "active" {
		return errors.New("rejected completion attempts changed the active isolation")
	}

	// Closing the second round keeps both rows and preserves full history.
	if _, err := completeCure(client, planURL, second.ID, contracts.CompleteCureRequest{
		RequestID:   "complete-request-002",
		CompletedBy: "officer-zhao",
	}); err != nil {
		return err
	}
	finalSnapshot, err := fetchCureSnapshot(client, planURL)
	if err != nil {
		return err
	}
	if len(finalSnapshot.Cures) != 2 {
		return errors.New("cure history must keep both completed rounds")
	}
	for _, value := range finalSnapshot.Cures {
		if value.Status != "completed" {
			return errors.New("a cure remains unfinished after both rounds completed")
		}
	}

	// Final progress closes the plan and stabilizes the specimen; the progress
	// history before, between and after isolations stays intact.
	if err := recordProgress(client, baseURL, plan.ID, 100, "修复完成"); err != nil {
		return err
	}
	doneSnapshot, err := fetchCureSnapshot(client, planURL)
	if err != nil {
		return err
	}
	if doneSnapshot.Plan.Status != "completed" || len(doneSnapshot.Progress) != 3 {
		return errors.New("plan did not complete after the second cure round")
	}
	if doneSnapshot.Progress[0].Percent != 40 ||
		doneSnapshot.Progress[1].Percent != 60 ||
		doneSnapshot.Progress[2].Percent != 100 {
		return errors.New("progress history around the isolations is incomplete")
	}
	var fetchedSpecimen specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
		return err
	}
	if fetchedSpecimen.Status != "stabilized" {
		return fmt.Errorf("expected stabilized specimen, got %s", fetchedSpecimen.Status)
	}
	return nil
}

func fetchCureSnapshot(client *http.Client, planURL string) (cureSnapshot, error) {
	var snapshot cureSnapshot
	if err := requestJSON(client, http.MethodGet, planURL, nil, http.StatusOK, &snapshot); err != nil {
		return cureSnapshot{}, err
	}
	return snapshot, nil
}

func startCure(
	client *http.Client,
	planURL string,
	request contracts.StartCureRequest,
	wantStatus int,
) (cureView, int, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return cureView{}, 0, err
	}
	httpRequest, err := http.NewRequest(http.MethodPost, planURL+"/cures", bytes.NewReader(encoded))
	if err != nil {
		return cureView{}, 0, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := client.Do(httpRequest)
	if err != nil {
		return cureView{}, 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return cureView{}, 0, err
	}
	if response.StatusCode != wantStatus {
		return cureView{}, response.StatusCode, fmt.Errorf("start cure returned %d: %s", response.StatusCode, string(raw))
	}
	var cure cureView
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cure); err != nil {
			return cureView{}, response.StatusCode, err
		}
	}
	return cure, response.StatusCode, nil
}

func completeCure(
	client *http.Client,
	planURL string,
	cureID string,
	request contracts.CompleteCureRequest,
) (cureView, error) {
	var cure cureView
	err := requestJSON(
		client,
		http.MethodPost,
		planURL+"/cures/"+cureID+"/completion",
		request,
		http.StatusOK,
		&cure,
	)
	return cure, err
}

func expectStatus(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(method, url, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != wantStatus {
		return fmt.Errorf("request %s %s returned %d, want %d: %s", method, url, response.StatusCode, wantStatus, string(raw))
	}
	return nil
}

func registerSpecimen(client *http.Client, baseURL string) (specimenView, error) {
	var specimen specimenView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: "PPS-2026-001",
			FieldLabel:    "北坡-07",
			TaxonHint:     "大型脊椎动物",
			MatrixKind:    "粉砂岩",
			JacketNotes:   "夹克边角完好",
		},
		http.StatusCreated,
		&specimen,
	)
	return specimen, err
}

func createPlan(client *http.Client, baseURL, specimenID string) (planView, error) {
	var plan planView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimenID+"/plans",
		contracts.CreatePlanRequest{
			Objective:      "暴露主要关节面",
			Method:         "手工显微剥离",
			Adhesive:       "可逆树脂",
			TargetExposure: 80,
		},
		http.StatusCreated,
		&plan,
	)
	return plan, err
}

func completedPlanSetup(client *http.Client, baseURL string) (specimenView, planView, error) {
	specimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	var approved planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		&approved,
	); err != nil {
		return specimenView{}, planView{}, err
	}
	return specimen, approved, nil
}

func recordProgress(client *http.Client, baseURL, planID string, percent int, stage string) error {
	var ignored struct{}
	return requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+planID+"/progress",
		contracts.ProgressRequest{
			Stage:      stage,
			Percent:    percent,
			Note:       "本地链路检查",
			RecordedBy: "preparator-chen",
		},
		http.StatusCreated,
		&ignored,
	)
}

func requestJSON(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
	target any,
) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, payload)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != wantStatus {
		return fmt.Errorf("request %s %s returned %d: %s", method, url, response.StatusCode, string(raw))
	}
	if target == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode response for %s %s: %w", method, url, err)
	}
	return nil
}
