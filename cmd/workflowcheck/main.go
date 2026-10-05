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
	Cures []cureView `json:"cures"`
}

type cureView struct {
	ID        string `json:"id"`
	PlanID    string `json:"plan_id"`
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

type errorView struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type releaseView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
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

func checkCureIsolation(client *http.Client, baseURL string) error {
	// 使用独立目录号，避免与其它链路检查共享事实。
	specimen, err := registerSpecimenAs(client, baseURL, "PPS-2026-CURE")
	if err != nil {
		return err
	}
	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if _, err := postExpect(
		client,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
	); err != nil {
		return err
	}
	// 加固阶段的普通进度，作为隔离前已存在的历史。
	if err := recordProgress(client, baseURL, plan.ID, 60, "表面加固"); err != nil {
		return err
	}

	start := contracts.StartCureRequest{
		RequestID: "start-req-001",
		StartedBy: "lead-wei",
		Note:      "进入密闭固化",
	}
	firstStart, err := postExpect(
		client,
		baseURL+"/plans/"+plan.ID+"/cures",
		start,
		http.StatusCreated,
	)
	if err != nil {
		return err
	}
	var cure cureView
	if err := json.Unmarshal(firstStart, &cure); err != nil {
		return err
	}
	if cure.Status != "active" || cure.PlanID != plan.ID || cure.RequestID != "start-req-001" {
		return fmt.Errorf("登记结果不是有效的隔离中记录: %s", string(firstStart))
	}
	activeCureID := cure.ID

	// 页面快照必须能看到这条隔离中的记录。
	snapshot, err := getPlanSnapshot(client, baseURL, plan.ID)
	if err != nil {
		return err
	}
	if !hasCure(snapshot.Cures, activeCureID, "active") {
		return errors.New("登记后页面看不到隔离中的记录")
	}

	// 同一登记请求重试/再次到达：只能对应同一份结果，不产生第二条隔离。
	replayedStart, err := postExpect(
		client,
		baseURL+"/plans/"+plan.ID+"/cures",
		start,
		http.StatusCreated,
	)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(replayedStart, &cure); err != nil {
		return err
	}
	if cure.ID != activeCureID {
		return fmt.Errorf("登记请求重放产生了新的隔离: %s != %s", cure.ID, activeCureID)
	}

	// 隔离期间用另一个登记请求开启新隔离必须冲突，保证同一时刻至多一个未结束隔离。
	if _, err := postExpect(
		client,
		baseURL+"/plans/"+plan.ID+"/cures",
		contracts.StartCureRequest{
			RequestID: "start-req-other",
			StartedBy: "lead-wei",
		},
		http.StatusConflict,
	); err != nil {
		return err
	}

	// 隔离期间提交进度必须明确失败（409），且不改变方案与历史。
	blockedProgress := contracts.ProgressRequest{
		Stage:      "隔离期补记",
		Percent:    80,
		RecordedBy: "preparator-chen",
	}
	if _, err := postExpect(
		client,
		baseURL+"/plans/"+plan.ID+"/progress",
		blockedProgress,
		http.StatusConflict,
	); err != nil {
		return err
	}
	// 失败请求重试也必须同样失败且无副作用。
	if _, err := postExpect(
		client,
		baseURL+"/plans/"+plan.ID+"/progress",
		blockedProgress,
		http.StatusConflict,
	); err != nil {
		return err
	}
	snapshot, err = getPlanSnapshot(client, baseURL, plan.ID)
	if err != nil {
		return err
	}
	if len(snapshot.Progress) != 1 || snapshot.Progress[0].Percent != 60 {
		return errors.New("隔离期间被拒的进度改变了历史或方案")
	}
	if snapshot.Plan.Status != "active" {
		return fmt.Errorf("隔离期间方案状态被意外改变: %s", snapshot.Plan.Status)
	}
	if !hasCure(snapshot.Cures, activeCureID, "active") {
		return errors.New("被拒的进度或重试留下/破坏了隔离状态")
	}

	// 另一名人员确认解除。
	completeReq := contracts.CompleteCureRequest{
		RequestID:   "complete-req-001",
		CompletedBy: "officer-he",
		Note:        "固化达标，解除隔离",
	}
	completion, err := postExpect(
		client,
		baseURL+"/plans/"+plan.ID+"/cures/"+activeCureID+"/completion",
		completeReq,
		http.StatusOK,
	)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(completion, &cure); err != nil {
		return err
	}
	if cure.ID != activeCureID || cure.Status != "completed" {
		return fmt.Errorf("解除结果不是同一条已完成隔离: %s", string(completion))
	}

	// 解除请求重放：返回同一条已完成记录，不追加悬空副本。
	replayedComplete, err := postExpect(
		client,
		baseURL+"/plans/"+plan.ID+"/cures/"+activeCureID+"/completion",
		completeReq,
		http.StatusOK,
	)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(replayedComplete, &cure); err != nil {
		return err
	}
	if cure.ID != activeCureID || cure.Status != "completed" {
		return fmt.Errorf("解除请求重放未返回同一条已完成隔离: %s", string(replayedComplete))
	}

	// 解除后页面不应再存在未结束隔离，且完整保留全部历史。
	snapshot, err = getPlanSnapshot(client, baseURL, plan.ID)
	if err != nil {
		return err
	}
	for _, record := range snapshot.Cures {
		if record.Status == "active" {
			return errors.New("解除完成后仍存在未结束隔离，后续进度会被阻塞")
		}
	}
	if !hasCure(snapshot.Cures, activeCureID, "completed") {
		return errors.New("解除后的隔离没有作为历史保留")
	}
	if len(snapshot.Progress) != 1 {
		return errors.New("隔离前后的进度历史未被完整保留")
	}

	// 解除成功后恢复继续记录，并可以走完第二轮固化。
	if err := recordProgress(client, baseURL, plan.ID, 80, "继续精修"); err != nil {
		return err
	}
	secondStart := contracts.StartCureRequest{
		RequestID: "start-req-002",
		StartedBy: "lead-wei",
		Note:      "二次固化",
	}
	secondStartBody, err := postExpect(
		client,
		baseURL+"/plans/"+plan.ID+"/cures",
		secondStart,
		http.StatusCreated,
	)
	if err != nil {
		return err
	}
	var secondCure cureView
	if err := json.Unmarshal(secondStartBody, &secondCure); err != nil {
		return err
	}
	if secondCure.ID == activeCureID || secondCure.Status != "active" {
		return errors.New("新一轮固化没有建立独立的隔离，上一轮状态被带入下一轮")
	}
	// 第二轮登记请求重放仍指向第二轮那一条。
	replayedSecond, err := postExpect(
		client,
		baseURL+"/plans/"+plan.ID+"/cures",
		secondStart,
		http.StatusCreated,
	)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(replayedSecond, &cure); err != nil {
		return err
	}
	if cure.ID != secondCure.ID {
		return errors.New("第二轮登记请求重放产生了额外隔离")
	}
	if _, err := postExpect(
		client,
		baseURL+"/plans/"+plan.ID+"/cures/"+secondCure.ID+"/completion",
		contracts.CompleteCureRequest{
			RequestID:   "complete-req-002",
			CompletedBy: "officer-he",
		},
		http.StatusOK,
	); err != nil {
		return err
	}

	// 第二轮解除后仍可记录到满进度，方案正常收口。
	if err := recordProgress(client, baseURL, plan.ID, 100, "修复完成"); err != nil {
		return err
	}
	snapshot, err = getPlanSnapshot(client, baseURL, plan.ID)
	if err != nil {
		return err
	}
	if snapshot.Plan.Status != "completed" {
		return fmt.Errorf("全部固化解除后方案未能正常完成: %s", snapshot.Plan.Status)
	}
	if len(snapshot.Progress) != 3 {
		return errors.New("跨两轮固化的进度历史不完整")
	}
	if len(snapshot.Cures) != 2 {
		return fmt.Errorf("应保留两条固化历史，实际 %d 条", len(snapshot.Cures))
	}
	for _, record := range snapshot.Cures {
		if record.Status != "completed" {
			return errors.New("最终快照中存在未结束的固化隔离")
		}
	}
	var fetched specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
		return err
	}
	if fetched.Status != "stabilized" {
		return fmt.Errorf("expected stabilized specimen, got %s", fetched.Status)
	}
	return nil
}

func registerSpecimenAs(client *http.Client, baseURL, catalog string) (specimenView, error) {
	var specimen specimenView
	body, err := postExpect(
		client,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: catalog,
			FieldLabel:    "北坡-固化",
			TaxonHint:     "大型脊椎动物",
			MatrixKind:    "粉砂岩",
			JacketNotes:   "固化链路检查",
		},
		http.StatusCreated,
	)
	if err != nil {
		return specimen, err
	}
	err = json.Unmarshal(body, &specimen)
	return specimen, err
}

func getPlanSnapshot(client *http.Client, baseURL, planID string) (planSnapshot, error) {
	var snapshot planSnapshot
	err := requestJSON(
		client,
		http.MethodGet,
		baseURL+"/plans/"+planID,
		nil,
		http.StatusOK,
		&snapshot,
	)
	return snapshot, err
}

func hasCure(cures []cureView, id string, status string) bool {
	for _, record := range cures {
		if record.ID == id && record.Status == status {
			return true
		}
	}
	return false
}

// postExpect issues a JSON POST and asserts the exact status, returning the body.
func postExpect(client *http.Client, url string, body any, wantStatus int) ([]byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != wantStatus {
		return nil, fmt.Errorf("POST %s returned %d, want %d: %s", url, response.StatusCode, wantStatus, string(raw))
	}
	return raw, nil
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
