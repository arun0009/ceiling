package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanPrescribesHoldsAndCaps(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pom.xml", `<project><artifactId>spring-boot-starter-data-jpa</artifactId></project>`)
	write(t, dir, "src/main/java/OrderService.java", `
package app;
import org.springframework.web.bind.annotation.RestController;
class OrderService {
    @Transactional
    void place() {
        restTemplate.getForObject("http://orders", String.class);
    }
}
`)
	write(t, dir, "src/main/java/OrderClient.java", `
class OrderClient {
    RestTemplate restTemplate = new RestTemplate();
}
`)
	write(t, dir, "src/main/java/CacheConfig.java", `
class CacheConfig {
    Object cache() {
        return Caffeine.newBuilder().expireAfterWrite(10).build();
    }
}
`)
	write(t, dir, "lib/service.ts", `
import { Construct } from "constructs";
service.autoScaleTaskCount({ minCapacity: 2, maxCapacity: 20 })
    .scaleOnCpuUtilization("cpu", { targetUtilizationPercent: 70 });
`)

	got := mustScan(t, dir)
	if !hasSuspect(got, "transaction-spans-http") {
		t.Fatal("expected transaction-spans-http")
	}
	if !hasSuspect(got, "open-in-view") {
		t.Fatal("expected open-in-view default")
	}
	if !hasSuspect(got, "outbound-no-timeout") {
		t.Fatal("expected missing timeout")
	}
	if !hasSuspect(got, "no-breaker") {
		t.Fatal("expected missing breaker")
	}
	if !hasSuspect(got, "unbounded-cache") {
		t.Fatal("expected unbounded cache")
	}
	if !hasSuspect(got, "cpu-scaling-for-io") {
		t.Fatal("expected cpu scaling on an I/O path")
	}
	if len(got.ScalingIntent) != 1 || got.ScalingIntent[0].Signal != "cpu" {
		t.Fatalf("scaling intent: %+v", got.ScalingIntent)
	}
}

func TestBoundedCacheAndClosedViewAreQuiet(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/main/java/CacheConfig.java", `
class CacheConfig {
    Object cache() {
        return Caffeine.newBuilder().maximumSize(1000).build();
    }
}
`)
	write(t, dir, "src/main/resources/application.properties", "spring.jpa.open-in-view=false\n")
	write(t, dir, "src/main/java/OrderService.java", `
import org.springframework.web.bind.annotation.RestController;
class OrderService {
    @Transactional
    void place() {
        repository.save(order);
    }
}
`)
	got := mustScan(t, dir)
	if hasSuspect(got, "unbounded-cache") || hasSuspect(got, "transaction-spans-http") || hasSuspect(got, "open-in-view") {
		t.Fatalf("quiet code produced %+v", got.Suspects)
	}
}

func TestPoolProfileMustBeExplicit(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/main/resources/application.yml", `
spring:
  datasource:
    url: jdbc:mysql://db/app
    hikari:
      maximum-pool-size: 10
`)
	write(t, dir, "src/main/resources/application-prod.yml", `
spring:
  datasource:
    hikari:
      maximum-pool-size: 20
`)
	unknown := mustScan(t, dir)
	if unknown.PoolMax != nil {
		t.Fatal("must not guess active profile")
	}
	got, err := scanProfile(dir, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if got.PoolMax == nil || *got.PoolMax != 20 {
		t.Fatalf("pool max %v source %s", got.PoolMax, got.PoolMaxSource)
	}
}

func TestClassTransactionHotPathAndBody(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/main/java/OrderService.java", `
@Transactional
class OrderService {
    void place() {
        restTemplate.getForObject("http://orders", String.class);
    }
}
`)
	write(t, dir, "src/hash.js", `const hash = bcrypt.hashSync(password, 12);`)
	write(t, dir, "src/app.js", `app.use(express.json({ limit: '20mb' }));`)
	got := mustScan(t, dir)
	if !hasSuspect(got, "transaction-spans-http") || !hasSuspect(got, "cpu-hot") || !hasSuspect(got, "unbounded-body") {
		t.Fatalf("%+v", got.Suspects)
	}
}

func TestSmallBodyIsQuiet(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/app.js", `app.use(express.json({ limit: '100kb' }));`)
	if hasSuspect(mustScan(t, dir), "unbounded-body") {
		t.Fatal("100kb body limit is a cap")
	}
}

func TestCPUScaleWithoutIOIsNotFlagged(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "lib/worker.ts", `worker.scaleOnCpuUtilization("cpu", { targetUtilizationPercent: 60 });`)
	got := mustScan(t, dir)
	if hasSuspect(got, "cpu-scaling-for-io") {
		t.Fatal("cpu scaling alone is not a finding")
	}
	if len(got.ScalingIntent) != 1 {
		t.Fatalf("intent %+v", got.ScalingIntent)
	}
}

func mustScan(t *testing.T, dir string) scanResult {
	t.Helper()
	got, err := scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func hasSuspect(got scanResult, id string) bool {
	for _, s := range got.Suspects {
		if s.ID == id {
			return true
		}
	}
	return false
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInactiveProfileDoesNotSupplyTimeout(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Client.java", `@FeignClient(name="inventory") interface Client {}`)
	write(t, dir, "application-test.properties", "feign.client.config.default.readTimeout=2000\n")
	got, err := scanProfile(dir, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if !hasSuspect(got, "outbound-no-timeout") {
		t.Fatal("inactive test settings hid missing production timeout")
	}
}

func TestConflictingSameProfilePoolsRemainUnknown(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a/application-prod.properties", "spring.datasource.hikari.maximum-pool-size=10\n")
	write(t, dir, "b/application-prod.properties", "spring.datasource.hikari.maximum-pool-size=20\n")
	got, err := scanProfile(dir, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if got.PoolMax != nil || len(got.Gaps) == 0 {
		t.Fatal("conflicting effective pool guessed")
	}
}
