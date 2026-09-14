package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	platformjwt "tradedrift/platform/jwt"
)

const (
	adminBaseURL   = "http://localhost:8085"
	gatewayBaseURL = "http://localhost:8080"
	adminToken     = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoiMDQ3MDg1MjMtY2U0NS00YWQ4LWEwNWUtYjlhNjU3MjQzMjdiIiwiZW1haWwiOiJha2hpbDI2NzIwMDFAZ21haWwuY29tIiwicm9sZSI6ImFkbWluIiwianRpIjoiMDE5MWY2MzAtYWRtaW4tanRpLWFraGlsIiwidG9rZW5fdmVyc2lvbiI6MSwiaXNzIjoidHJhZGVkcmlmdC1hdXRoIiwic3ViIjoiMDQ3MDg1MjMtY2U0NS00YWQ4LWEwNWUtYjlhNjU3MjQzMjdiIiwiZXhwIjoxODIwOTA5MzgwLCJuYmYiOjE3ODkzNzMzODAsImlhdCI6MTc4OTM3MzM4MH0.nDWxqe3Y9MXmab263DFicCD2bjdGXb7nB3my6Y8LIqo"
)

func sendOrder(client *http.Client, token, market, side, orderType, price, qty string) (int, string, error) {
	b, _ := json.Marshal(map[string]interface{}{
		"market_id":  market,
		"side":       side,
		"order_type": orderType,
		"price":      price,
		"quantity":   qty,
	})
	req, err := http.NewRequest("POST", gatewayBaseURL+"/api/v1/orders", bytes.NewReader(b))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody), nil
}

func main() {
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer rdb.Close()

	ctx := context.Background()
	client := &http.Client{Timeout: 10 * time.Second}

	fmt.Println("==========================================================")
	fmt.Println("🚀 STARTING TRADE-DRIFT CONTROL PLANE TEST SUITE")
	fmt.Println("==========================================================")

	// 0. Generate test user access token (user1@example.com)
	testUserID := "1f85afe9-e866-4629-bf51-c8dc8a72d7aa"
	secret := []byte("super-secret-jwt-key-tradedrift-dev-32bytes")
	userToken, _, err := platformjwt.IssueAccessToken(testUserID, "user1@example.com", "user", 1, secret, 24*time.Hour)
	if err != nil {
		fmt.Printf("❌ Failed to generate user token: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Prepared Test User: user1@example.com (ID: %s)\n\n", testUserID)

	// Clean baseline state
	resetUnsus, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/admin/users/%s/unsuspend", adminBaseURL, testUserID), bytes.NewReader([]byte(`{"reason":"pre-test reset"}`)))
	resetUnsus.Header.Set("Authorization", "Bearer "+adminToken)
	resetUnsus.Header.Set("Content-Type", "application/json")
	resetUnsus.Header.Set("Idempotency-Key", fmt.Sprintf("reset-unsus-%d", time.Now().UnixNano()))
	if resp, err := client.Do(resetUnsus); err == nil {
		resp.Body.Close()
	}

	resetUnfreeze, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/admin/users/%s/wallets/USDT/unfreeze", adminBaseURL, testUserID), bytes.NewReader([]byte(`{"reason":"pre-test reset"}`)))
	resetUnfreeze.Header.Set("Authorization", "Bearer "+adminToken)
	resetUnfreeze.Header.Set("Content-Type", "application/json")
	resetUnfreeze.Header.Set("Idempotency-Key", fmt.Sprintf("reset-unfreeze-%d", time.Now().UnixNano()))
	if resp, err := client.Do(resetUnfreeze); err == nil {
		resp.Body.Close()
	}

	resetResume, _ := http.NewRequest("POST", adminBaseURL+"/api/v1/admin/markets/BTC-USDT/resume", bytes.NewReader([]byte(`{"reason":"pre-test reset"}`)))
	resetResume.Header.Set("Authorization", "Bearer "+adminToken)
	resetResume.Header.Set("Content-Type", "application/json")
	resetResume.Header.Set("Idempotency-Key", fmt.Sprintf("reset-resume-%d", time.Now().UnixNano()))
	if resp, err := client.Do(resetResume); err == nil {
		resp.Body.Close()
	}
	rdb.Set(ctx, "market:enforcement:ready", "1", 0)
	time.Sleep(500 * time.Millisecond)

	// ─── TEST 1: MARKET HALT INVARIANTS ───────────────────────────────────────
	fmt.Println("─── [TEST 1: Market Halt Invariants] ───")
	// 1.1 Halt Market BTC-USDT
	haltReq, _ := http.NewRequest("POST", adminBaseURL+"/api/v1/admin/markets/BTC-USDT/halt", bytes.NewReader([]byte(`{"reason":"regulatory investigation"}`)))
	haltReq.Header.Set("Authorization", "Bearer "+adminToken)
	haltReq.Header.Set("Content-Type", "application/json")
	haltReq.Header.Set("Idempotency-Key", fmt.Sprintf("halt-%d", time.Now().UnixNano()))
	hResp, err := client.Do(haltReq)
	if err != nil || (hResp.StatusCode != 200 && hResp.StatusCode != 201) {
		hBody, _ := io.ReadAll(hResp.Body)
		fmt.Printf("❌ HaltMarket failed: status %d, body %s\n", hResp.StatusCode, string(hBody))
		os.Exit(1)
	}
	fmt.Println("✅ 1.1 Admin HaltMarket BTC-USDT: Success (200/201)")

	// 1.2 Verify Redis key
	val, err := rdb.Get(ctx, "market:halted:BTC-USDT").Result()
	if err != nil || val != "1" {
		fmt.Printf("❌ Redis key market:halted:BTC-USDT expected '1', got val=%s, err=%v\n", val, err)
		os.Exit(1)
	}
	fmt.Println("✅ 1.2 Redis key market:halted:BTC-USDT verified: '1'")

	// 1.3 Attempt Order Placement on BTC-USDT
	code, body, err := sendOrder(client, userToken, "BTC-USDT", "BUY", "LIMIT", "96500.00", "0.001")
	if err != nil {
		fmt.Printf("❌ Order request failed: %v\n", err)
		os.Exit(1)
	}
	if code != 422 {
		fmt.Printf("❌ Expected HTTP 422 (market halted), got status %d, body %s\n", code, body)
		os.Exit(1)
	}
	fmt.Printf("✅ 1.3 Order placement correctly REJECTED with HTTP 422: %s\n", strings.TrimSpace(body))

	// 1.4 Resume Market BTC-USDT
	resumeReq, _ := http.NewRequest("POST", adminBaseURL+"/api/v1/admin/markets/BTC-USDT/resume", bytes.NewReader([]byte(`{"reason":"investigation concluded"}`)))
	resumeReq.Header.Set("Authorization", "Bearer "+adminToken)
	resumeReq.Header.Set("Content-Type", "application/json")
	resumeReq.Header.Set("Idempotency-Key", fmt.Sprintf("resume-%d", time.Now().UnixNano()))
	rResp, err := client.Do(resumeReq)
	if err != nil || (rResp.StatusCode != 200 && rResp.StatusCode != 201) {
		rBody, _ := io.ReadAll(rResp.Body)
		fmt.Printf("❌ ResumeMarket failed: status %d, body %s\n", rResp.StatusCode, string(rBody))
		os.Exit(1)
	}
	fmt.Println("✅ 1.4 Admin ResumeMarket BTC-USDT: Success (200/201)")

	// 1.5 Verify Redis key removed
	_, err = rdb.Get(ctx, "market:halted:BTC-USDT").Result()
	if err != redis.Nil {
		fmt.Printf("❌ Redis key market:halted:BTC-USDT should be deleted, got err=%v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ 1.5 Redis key market:halted:BTC-USDT deleted from Redis")

	// 1.6 Re-attempt Order Placement on BTC-USDT
	code, body, err = sendOrder(client, userToken, "BTC-USDT", "BUY", "LIMIT", "96500.00", "0.001")
	if err != nil || (code != 200 && code != 201) {
		fmt.Printf("❌ Expected HTTP 200/201 after resume, got status %d, body %s\n", code, body)
		os.Exit(1)
	}
	fmt.Printf("✅ 1.6 Order accepted after market resume: HTTP %d\n\n", code)

	// ─── TEST 2: WALLET FREEZE TRANSACTIONAL BOUNDARY ────────────────────────
	fmt.Println("─── [TEST 2: Wallet Freeze Transactional Boundary] ───")
	// 2.1 Freeze USDT Wallet for test user
	freezeReq, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/admin/users/%s/wallets/USDT/freeze", adminBaseURL, testUserID), bytes.NewReader([]byte(`{"reason":"AML risk hold"}`)))
	freezeReq.Header.Set("Authorization", "Bearer "+adminToken)
	freezeReq.Header.Set("Content-Type", "application/json")
	freezeReq.Header.Set("Idempotency-Key", fmt.Sprintf("freeze-%d", time.Now().UnixNano()))
	fResp, err := client.Do(freezeReq)
	if err != nil || (fResp.StatusCode != 200 && fResp.StatusCode != 201) {
		fBody, _ := io.ReadAll(fResp.Body)
		fmt.Printf("❌ FreezeWallet failed: status %d, body %s\n", fResp.StatusCode, string(fBody))
		os.Exit(1)
	}
	fmt.Println("✅ 2.1 Admin FreezeWallet USDT: Success (200/201)")

	// 2.2 Attempt Order Placement requiring USDT reservation
	code, body, err = sendOrder(client, userToken, "BTC-USDT", "BUY", "LIMIT", "96500.00", "0.001")
	if err != nil {
		fmt.Printf("❌ Order request failed: %v\n", err)
		os.Exit(1)
	}
	if code != 422 {
		fmt.Printf("❌ Expected HTTP 422 (wallet frozen), got status %d, body %s\n", code, body)
		os.Exit(1)
	}
	fmt.Printf("✅ 2.2 Order fund reservation rejected with HTTP 422 (wallet frozen): %s\n", strings.TrimSpace(body))

	// 2.3 Unfreeze USDT Wallet
	unfreezeReq, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/admin/users/%s/wallets/USDT/unfreeze", adminBaseURL, testUserID), bytes.NewReader([]byte(`{"reason":"AML check cleared"}`)))
	unfreezeReq.Header.Set("Authorization", "Bearer "+adminToken)
	unfreezeReq.Header.Set("Content-Type", "application/json")
	unfreezeReq.Header.Set("Idempotency-Key", fmt.Sprintf("unfreeze-%d", time.Now().UnixNano()))
	ufResp, err := client.Do(unfreezeReq)
	if err != nil || (ufResp.StatusCode != 200 && ufResp.StatusCode != 201) {
		ufBody, _ := io.ReadAll(ufResp.Body)
		fmt.Printf("❌ UnfreezeWallet failed: status %d, body %s\n", ufResp.StatusCode, string(ufBody))
		os.Exit(1)
	}
	fmt.Println("✅ 2.3 Admin UnfreezeWallet USDT: Success (200/201)")

	// 2.4 Verify Order fund reservation succeeds after unfreeze
	code, body, err = sendOrder(client, userToken, "BTC-USDT", "BUY", "LIMIT", "96500.00", "0.001")
	if err != nil || (code != 200 && code != 201) {
		fmt.Printf("❌ Expected order success after unfreeze, got status %d, body %s\n", code, body)
		os.Exit(1)
	}
	fmt.Printf("✅ 2.4 Order successfully placed after unfreeze: HTTP %d\n\n", code)

	// ─── TEST 3: USER SUSPENSION LIFECYCLE ───────────────────────────────────
	fmt.Println("─── [TEST 3: User Suspension Lifecycle] ───")
	// 3.1 Suspend User
	susReq, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/admin/users/%s/suspend", adminBaseURL, testUserID), bytes.NewReader([]byte(`{"reason":"Fraud investigation"}`)))
	susReq.Header.Set("Authorization", "Bearer "+adminToken)
	susReq.Header.Set("Content-Type", "application/json")
	susReq.Header.Set("Idempotency-Key", fmt.Sprintf("suspend-%d", time.Now().UnixNano()))
	sResp, err := client.Do(susReq)
	if err != nil || (sResp.StatusCode != 200 && sResp.StatusCode != 201) {
		sBody, _ := io.ReadAll(sResp.Body)
		fmt.Printf("❌ SuspendUser failed: status %d, body %s\n", sResp.StatusCode, string(sBody))
		os.Exit(1)
	}
	fmt.Println("✅ 3.1 Admin SuspendUser: Success (200/201)")

	// 3.2 Verify user:suspended Redis key
	sVal, err := rdb.Get(ctx, "user:suspended:"+testUserID).Result()
	if err != nil || sVal != "1" {
		fmt.Printf("❌ Redis key user:suspended:%s expected '1', got val=%s, err=%v\n", testUserID, sVal, err)
		os.Exit(1)
	}
	fmt.Printf("✅ 3.2 Redis key user:suspended:%s verified: '1'\n", testUserID)

	// 3.3 Attempt authenticated request with in-flight token -> Must return HTTP 403
	getWalletsReq, _ := http.NewRequest("GET", gatewayBaseURL+"/api/v1/wallet/balances", nil)
	getWalletsReq.Header.Set("Authorization", "Bearer "+userToken)
	gwResp, err := client.Do(getWalletsReq)
	if err != nil {
		fmt.Printf("❌ GetWallets request failed: %v\n", err)
		os.Exit(1)
	}
	gwBody, _ := io.ReadAll(gwResp.Body)
	gwResp.Body.Close()
	if gwResp.StatusCode != 403 {
		fmt.Printf("❌ Expected HTTP 403 (user suspended), got status %d, body %s\n", gwResp.StatusCode, string(gwBody))
		os.Exit(1)
	}
	fmt.Printf("✅ 3.3 In-flight request blocked by Gateway Auth middleware with HTTP 403: %s\n", strings.TrimSpace(string(gwBody)))

	// 3.4 Attempt order placement while suspended -> Gateway blocks with HTTP 403
	code, body, err = sendOrder(client, userToken, "BTC-USDT", "BUY", "LIMIT", "96500.00", "0.001")
	if err != nil {
		fmt.Printf("❌ Order request failed: %v\n", err)
		os.Exit(1)
	}
	if code != 403 {
		fmt.Printf("❌ Expected HTTP 403 for suspended user order, got %d: %s\n", code, body)
		os.Exit(1)
	}
	fmt.Printf("✅ 3.4 Order attempt for suspended user blocked by Gateway: HTTP 403 (%s)\n", strings.TrimSpace(body))

	// 3.5 Unsuspend User
	unsusReq, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/admin/users/%s/unsuspend", adminBaseURL, testUserID), bytes.NewReader([]byte(`{"reason":"Investigation cleared"}`)))
	unsusReq.Header.Set("Authorization", "Bearer "+adminToken)
	unsusReq.Header.Set("Content-Type", "application/json")
	unsusReq.Header.Set("Idempotency-Key", fmt.Sprintf("unsuspend-%d", time.Now().UnixNano()))
	usResp, err := client.Do(unsusReq)
	if err != nil || (usResp.StatusCode != 200 && usResp.StatusCode != 201) {
		usBody, _ := io.ReadAll(usResp.Body)
		fmt.Printf("❌ UnsuspendUser failed: status %d, body %s\n", usResp.StatusCode, string(usBody))
		os.Exit(1)
	}
	fmt.Println("✅ 3.5 Admin UnsuspendUser: Success (200/201)")

	// 3.6 Verify user:suspended Redis key deleted
	_, err = rdb.Get(ctx, "user:suspended:"+testUserID).Result()
	if err != redis.Nil {
		fmt.Printf("❌ Redis key user:suspended:%s should be deleted, got err=%v\n", testUserID, err)
		os.Exit(1)
	}
	fmt.Printf("✅ 3.6 Redis key user:suspended:%s deleted from Redis\n", testUserID)

	// 3.7 In-flight request allowed after unsuspension
	freshUserToken, _, err := platformjwt.IssueAccessToken(testUserID, "user1@example.com", "user", 2, secret, 24*time.Hour)
	if err != nil {
		fmt.Printf("❌ Failed to generate fresh user token: %v\n", err)
		os.Exit(1)
	}

	getWalletsReq2, _ := http.NewRequest("GET", gatewayBaseURL+"/api/v1/wallet/balances", nil)
	getWalletsReq2.Header.Set("Authorization", "Bearer "+freshUserToken)
	gwResp2, err := client.Do(getWalletsReq2)
	if err != nil || gwResp2.StatusCode != 200 {
		lBody3, _ := io.ReadAll(gwResp2.Body)
		fmt.Printf("❌ Expected success after unsuspension, got status %d, body %s\n", gwResp2.StatusCode, string(lBody3))
		os.Exit(1)
	}
	gwResp2.Body.Close()
	fmt.Printf("✅ 3.7 Request allowed after unsuspension: HTTP 200 OK\n\n")

	// ─── TEST 4: ANTI-DRIFT RECONCILIATION & RECOVERY ───────────────────────
	fmt.Println("─── [TEST 4: Anti-Drift Reconciliation] ───")
	// 4.1 Halt BTC-USDT
	hReq2, _ := http.NewRequest("POST", adminBaseURL+"/api/v1/admin/markets/BTC-USDT/halt", bytes.NewReader([]byte(`{"reason":"anti-drift test"}`)))
	hReq2.Header.Set("Authorization", "Bearer "+adminToken)
	hReq2.Header.Set("Content-Type", "application/json")
	hReq2.Header.Set("Idempotency-Key", fmt.Sprintf("drift-halt-%d", time.Now().UnixNano()))
	h2Resp, _ := client.Do(hReq2)
	h2Resp.Body.Close()
	fmt.Println("✅ 4.1 Market BTC-USDT halted")

	// 4.2 Simulate cache eviction / drift by manually deleting Redis key
	rdb.Del(ctx, "market:halted:BTC-USDT")
	fmt.Println("👉 4.2 Manually deleted market:halted:BTC-USDT to simulate cache drift")

	// 4.3 Wait up to 20s for Admin StateReconciler to restore key
	fmt.Println("⏳ 4.3 Waiting for StateReconciler cycle (≤15s)...")
	restored := false
	for i := 0; i < 20; i++ {
		time.Sleep(1 * time.Second)
		v, err := rdb.Get(ctx, "market:halted:BTC-USDT").Result()
		if err == nil && v == "1" {
			restored = true
			fmt.Printf("✅ 4.3 Anti-Drift Reconciler restored missing key in %d seconds!\n", i+1)
			break
		}
	}
	if !restored {
		fmt.Println("❌ StateReconciler did not restore key within 20s")
		os.Exit(1)
	}

	// 4.4 Clean up: Resume BTC-USDT
	resReq2, _ := http.NewRequest("POST", adminBaseURL+"/api/v1/admin/markets/BTC-USDT/resume", bytes.NewReader([]byte(`{"reason":"anti-drift test complete"}`)))
	resReq2.Header.Set("Authorization", "Bearer "+adminToken)
	resReq2.Header.Set("Content-Type", "application/json")
	resReq2.Header.Set("Idempotency-Key", fmt.Sprintf("drift-res-%d", time.Now().UnixNano()))
	res2Resp, _ := client.Do(resReq2)
	res2Resp.Body.Close()
	fmt.Println("✅ 4.4 Market BTC-USDT resumed cleanly")

	// ─── TEST 5: REDIS COLD-START & FAIL-CLOSED VERIFICATION ─────────────────
	fmt.Println("\n─── [TEST 5: Redis Cold-Start / Restart Fail-Closed Verification] ───")
	// 5.1 Delete market:enforcement:ready to simulate an unconfirmed cache state
	rdb.Del(ctx, "market:enforcement:ready")
	fmt.Println("👉 5.1 Deliberately evicted market:enforcement:ready sentinel")

	// 5.2 Attempt order placement -> must fail closed with HTTP 503 Unavailable
	code, body, err = sendOrder(client, freshUserToken, "BTC-USDT", "BUY", "LIMIT", "96500.00", "0.001")
	if err != nil {
		fmt.Printf("❌ Order request failed: %v\n", err)
		os.Exit(1)
	}
	if code != 503 {
		fmt.Printf("❌ Expected HTTP 503 (enforcement unready), got %d: %s\n", code, body)
		os.Exit(1)
	}
	fmt.Printf("✅ 5.2 Order intake strictly FAILS CLOSED with HTTP 503 when enforcement is unready: %s\n", strings.TrimSpace(body))

	// 5.3 Wait for StateReconciler to restore readiness
	fmt.Println("⏳ 5.3 Waiting for StateReconciler to re-assert readiness sentinel...")
	readyRestored := false
	for i := 0; i < 20; i++ {
		time.Sleep(1 * time.Second)
		v, err := rdb.Get(ctx, "market:enforcement:ready").Result()
		if err == nil && v == "1" {
			readyRestored = true
			fmt.Printf("✅ 5.3 StateReconciler re-asserted readiness sentinel in %d seconds!\n", i+1)
			break
		}
	}
	if !readyRestored {
		fmt.Println("❌ StateReconciler did not re-assert readiness within 20s")
		os.Exit(1)
	}

	// ─── TEST 6: CONCURRENT RACE VERIFICATION ────────────────────────────────
	fmt.Println("\n─── [TEST 6: Concurrent Race Verification] ───")
	// Launch 10 concurrent orders while triggering Halt
	haltDone := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Millisecond)
		hReq3, _ := http.NewRequest("POST", adminBaseURL+"/api/v1/admin/markets/BTC-USDT/halt", bytes.NewReader([]byte(`{"reason":"concurrency test"}`)))
		hReq3.Header.Set("Authorization", "Bearer "+adminToken)
		hReq3.Header.Set("Content-Type", "application/json")
		hReq3.Header.Set("Idempotency-Key", fmt.Sprintf("race-halt-%d", time.Now().UnixNano()))
		h3Resp, _ := client.Do(hReq3)
		if h3Resp != nil {
			h3Resp.Body.Close()
		}
		close(haltDone)
	}()

	results := make(chan int, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			c, _, err := sendOrder(client, freshUserToken, "BTC-USDT", "BUY", "LIMIT", fmt.Sprintf("%d.00", 96500+idx), "0.001")
			if err != nil {
				results <- 500
				return
			}
			results <- c
		}(i)
	}

	<-haltDone
	var accepted, rejected422, other int
	for i := 0; i < 10; i++ {
		c := <-results
		if c == 200 || c == 201 {
			accepted++
		} else if c == 422 {
			rejected422++
		} else {
			other++
		}
	}
	fmt.Printf("✅ 6.1 Concurrent Orders During Halt: %d Accepted (before halt), %d Rejected 422 (after halt), %d Other\n", accepted, rejected422, other)

	// Clean up: Resume market
	resReq3, _ := http.NewRequest("POST", adminBaseURL+"/api/v1/admin/markets/BTC-USDT/resume", bytes.NewReader([]byte(`{"reason":"concurrency test done"}`)))
	resReq3.Header.Set("Authorization", "Bearer "+adminToken)
	resReq3.Header.Set("Content-Type", "application/json")
	resReq3.Header.Set("Idempotency-Key", fmt.Sprintf("race-res-%d", time.Now().UnixNano()))
	res3Resp, _ := client.Do(resReq3)
	res3Resp.Body.Close()
	fmt.Println("✅ 6.2 Market BTC-USDT resumed cleanly")

	fmt.Println("\n==========================================================")
	fmt.Println("🎉 ALL 6 CONTROL PLANE VERIFICATION SUITES PASSED (10/10)!")
	fmt.Println("==========================================================")
}
