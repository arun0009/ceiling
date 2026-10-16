package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const maxFileBytes = 512 * 1024

// Library defaults the scan records by name. The report must say they are defaults.
const (
	hikariDefaultMax = 10
	pgDefaultMax     = 10
)

var (
	skipDirs = map[string]struct{}{
		"__tests__": {}, "bin": {}, "build": {}, "coverage": {}, "dist": {},
		"node_modules": {}, "out": {}, "spec": {}, "target": {}, "test": {},
		"tests": {}, "vendor": {},
	}
	sourceExt = map[string]struct{}{
		".cjs": {}, ".java": {}, ".js": {}, ".jsx": {}, ".kt": {},
		".mjs": {}, ".ts": {}, ".tsx": {},
	}
	queryPattern = regexp.MustCompile(
		`(repository\.|Repository\.|jdbcTemplate|entityManager|prisma\.|knex\(|\.findBy[A-Z]|createQuery\()`,
	)
	nodeHTTPPattern = regexp.MustCompile(`\b(axios(\.\w+)?|fetch|got|undici|node-fetch)\s*[\.\(]`)
	nodeTimeout     = regexp.MustCompile(`(\btimeout\s*:|\bsignal\s*:|AbortSignal|\.setTimeout\(|headersTimeout|requestTimeout)`)
	breakerPattern  = regexp.MustCompile(`(@CircuitBreaker\b|CircuitBreaker\.of\b|\bopossum\b|\bcockatiel\b|new\s+CircuitBreaker\b)`)
	poolCtor        = regexp.MustCompile(`\b(new\s+Pool|createPool|createConnection)\s*\(`)
	maxField        = regexp.MustCompile(`\b(?:max|connectionLimit)\s*:\s*(\d+)`)
	handlerPattern  = regexp.MustCompile(`(exports\.handler|module\.exports\.handler|export\s+(?:async\s+)?(?:function|const)\s+handler)\b`)
	fanoutPattern   = regexp.MustCompile(`Promise\.all\(\s*[A-Za-z0-9_$.]*\.map\s*\(`)
	cpuHot          = regexp.MustCompile(`(?i)\b(bcrypt|argon2|scrypt|pbkdf2|hashSync|pbkdf2Sync)\b`)
	bodyLimit       = regexp.MustCompile(`(?i)limit\s*:\s*['\"]?(\d+)\s*(kb|mb|gb)?`)
	javaClients     = []struct {
		needle string
		holds  string
	}{
		{"RestTemplate", "thread"},
		{"RestClient", "thread"},
		{"@FeignClient", "thread"},
		{"WebClient", "subscription"},
		{"HttpClient.newBuilder", "subscription"},
		{"HttpClient.newHttpClient", "subscription"},
		{"OkHttpClient", "thread"},
	}
)

type scanResult struct {
	Scope            string          `json:"scope"`
	AppProfile       string          `json:"app_profile,omitempty"`
	Gaps             []string        `json:"gaps"`
	Languages        []string        `json:"languages"`
	Platforms        []string        `json:"platforms"`
	Modules          []string        `json:"modules,omitempty"`
	MultiModule      bool            `json:"multi_module"`
	JavaModel        string          `json:"java_model"`
	Datastores       []string        `json:"datastores"`
	Libraries        []string        `json:"libraries,omitempty"`
	NodeProcesses    string          `json:"node_processes"`
	PoolMax          *int            `json:"pool_max,omitempty"`
	PoolMaxSource    string          `json:"pool_max_source,omitempty"`
	PoolNote         string          `json:"pool_note,omitempty"`
	PoolTimeoutRaw   string          `json:"pool_timeout_raw,omitempty"`
	ThreadPoolMax    *int            `json:"thread_pool_max,omitempty"`
	OutboundTimeouts []named         `json:"outbound_timeouts,omitempty"`
	Breaker          bool            `json:"breaker"`
	Retry            *retry          `json:"retry,omitempty"`
	ScalingIntent    []scalingIntent `json:"scaling_intent,omitempty"`
	Calls            []call          `json:"calls"`
	Suspects         []suspect       `json:"suspects"`
}

// scalingIntent is how the repo asks the service to scale. It is not a verdict.
type scalingIntent struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Signal string `json:"signal"`
	Call   string `json:"call"`
}

type named struct {
	Source string `json:"source"`
	Raw    string `json:"raw"`
}

type retry struct {
	Field  string `json:"field"`
	Value  int    `json:"value"`
	Source string `json:"source"`
}

type call struct {
	File              string `json:"file"`
	Line              int    `json:"line"`
	Client            string `json:"client"`
	Holds             string `json:"holds"`
	TimeoutInFile     bool   `json:"timeout_in_file"`
	TimeoutConfigured bool   `json:"timeout_configured"`
	BreakerInFile     bool   `json:"breaker_in_file"`
}

type suspect struct {
	ID        string     `json:"id"`
	Fact      string     `json:"fact"`
	Confirm   string     `json:"confirm"`
	Locations []location `json:"locations"`
	Count     int        `json:"count"`
}

type location struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Client string `json:"client"`
}

type poolHit struct {
	identity string
	kind     string
	max      int
	profile  string
	source   string
}

type cfgKey struct {
	key     string
	raw     string
	profile string
	file    string
}

type scanner struct {
	profile     string
	gaps        []string
	languages   map[string]struct{}
	platforms   map[string]struct{}
	datastores  map[string]struct{}
	libraries   map[string]struct{}
	modules     map[string]struct{}
	keys        []cfgKey
	pools       []poolHit
	calls       []call
	locs        map[string][]location
	retries     []retry
	javaMVC     bool
	javaFlux    bool
	nodeCluster bool
	nodeWorkers bool
	breakerCode bool
	datasource  bool
	jpa         bool
	scaling     []scalingIntent
}

func scan(dir string) (scanResult, error) { return scanProfile(dir, "") }

func scanProfile(dir, profile string) (scanResult, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return scanResult{}, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return scanResult{}, fmt.Errorf("not a directory: %s", dir)
	}
	s := &scanner{profile: profile, gaps: []string{},
		languages:  map[string]struct{}{},
		platforms:  map[string]struct{}{},
		datastores: map[string]struct{}{},
		libraries:  map[string]struct{}{},
		modules:    map[string]struct{}{},
		locs:       map[string][]location{},
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			s.gaps = append(s.gaps, "unreadable path: "+path)
			return nil
		}
		if d.IsDir() {
			if path != root && (skipDir(d.Name()) || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			s.gaps = append(s.gaps, "symlink not followed: "+path)
			return nil
		}
		if skipFile(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		text, err := readText(path)
		if err != nil {
			s.gaps = append(s.gaps, "unreadable or oversized file: "+rel)
			return nil
		}
		if text == "" {
			return nil
		}
		s.absorb(rel, text)
		return nil
	})
	if err != nil {
		return scanResult{}, err
	}
	return s.result(), nil
}

func (s *scanner) absorb(rel, text string) {
	base := filepath.Base(rel)
	ext := strings.ToLower(filepath.Ext(base))
	switch {
	case base == "pom.xml":
		s.absorbPom(rel, text)
		return
	case base == "build.gradle" || base == "build.gradle.kts":
		s.absorbGradle(rel, text)
		return
	case base == "package.json":
		s.absorbPackage(rel, text)
		return
	case base == "Dockerfile":
		s.platforms["container"] = struct{}{}
		return
	case base == "serverless.yml" || base == "serverless.yaml":
		s.platforms["lambda"] = struct{}{}
		s.absorbYAMLConfig(rel, "template", text)
		return
	case base == "template.yaml" || base == "template.yml":
		if strings.Contains(text, "AWS::Serverless::Function") || strings.Contains(text, "AWS::Lambda::Function") {
			s.platforms["lambda"] = struct{}{}
		}
		if strings.Contains(text, "AWS::ECS::Service") || strings.Contains(text, "FARGATE") {
			s.platforms["ecs"] = struct{}{}
		}
		s.absorbYAMLConfig(rel, "template", text)
		return
	case strings.HasPrefix(base, "application") && (ext == ".yml" || ext == ".yaml" || ext == ".properties"):
		s.absorbAppConfig(rel, text, ext)
		return
	}
	if ext == ".py" && (strings.Contains(text, "aws_cdk") || strings.Contains(text, "scale_on_")) {
		s.noteScaling(rel, text)
		return
	}
	if _, ok := sourceExt[ext]; !ok {
		return
	}
	switch ext {
	case ".java", ".kt":
		if ext == ".java" {
			s.languages["java"] = struct{}{}
		} else {
			s.languages["kotlin"] = struct{}{}
		}
	default:
		s.languages["node"] = struct{}{}
	}
	s.absorbSource(rel, text)
}

func (s *scanner) absorbSource(rel, text string) {
	code := codeText(text)
	if strings.Contains(code, "org.springframework.web.reactive") {
		s.javaFlux = true
	}
	if strings.Contains(code, "@RestController") || strings.Contains(code, "org.springframework.web.bind.annotation") {
		s.javaMVC = true
	}
	if strings.Contains(code, "cluster.fork(") || strings.Contains(code, "node:cluster") || strings.Contains(code, "require('cluster')") || strings.Contains(code, `require("cluster")`) {
		s.nodeCluster = true
	}
	if strings.Contains(code, "worker_threads") {
		s.nodeWorkers = true
	}
	if breakerPattern.MatchString(code) {
		s.breakerCode = true
	}
	if strings.Contains(code, "jdbc:postgresql") {
		s.datastores["postgres"] = struct{}{}
	}
	if strings.Contains(code, "jdbc:mysql") {
		s.datastores["mysql"] = struct{}{}
	}
	if strings.Contains(strings.ToLower(code), "dynamodb") {
		s.datastores["dynamodb"] = struct{}{}
	}
	if strings.Contains(code, "EntityManager") || strings.Contains(code, "JpaRepository") {
		s.jpa = true
	}
	s.noteRetries(rel, code)
	s.notePools(rel, code)
	s.noteCalls(rel, text, code)
	s.noteTransaction(rel, text)
	s.noteCache(rel, text)
	s.noteHotPath(rel, text)
	s.noteScaling(rel, text)
	s.noteLoop(rel, text)
	s.noteFanout(rel, code)
	s.noteHandlerConnection(rel, text)
	if strings.Contains(code, "newCachedThreadPool") {
		s.addLoc("unbounded-thread-pool", rel, lineOf(text, "newCachedThreadPool"), "newCachedThreadPool")
	}
	if strings.Contains(code, "parallelStream()") {
		s.addLoc("parallel-stream", rel, lineOf(text, "parallelStream()"), "parallelStream")
	}
}

func (s *scanner) noteCalls(rel, original, code string) {
	timeoutHere := strings.Contains(code, "setReadTimeout") ||
		strings.Contains(code, "setConnectTimeout") ||
		strings.Contains(code, "responseTimeout") ||
		strings.Contains(code, "ReadTimeoutHandler") ||
		strings.Contains(code, ".readTimeout(") ||
		strings.Contains(code, ".connectTimeout(") ||
		strings.Contains(code, ".callTimeout(") ||
		nodeTimeout.MatchString(code)
	breakerHere := breakerPattern.MatchString(code)
	for _, client := range javaClients {
		if !strings.Contains(code, client.needle) {
			continue
		}
		holds := client.holds
		if client.needle == "WebClient" && strings.Contains(code, ".block(") {
			holds = "thread"
		}
		name := strings.TrimPrefix(client.needle, "@")
		s.calls = append(s.calls, call{
			File:          rel,
			Line:          lineOf(original, client.needle),
			Client:        name,
			Holds:         holds,
			TimeoutInFile: timeoutHere,
			BreakerInFile: breakerHere,
		})
	}
	if nodeHTTPPattern.MatchString(code) {
		s.calls = append(s.calls, call{
			File:          rel,
			Line:          lineOf(original, nodeHTTPPattern),
			Client:        "node-http",
			Holds:         "socket",
			TimeoutInFile: timeoutHere,
			BreakerInFile: breakerHere,
		})
	}
}

func (s *scanner) notePools(rel, code string) {
	lines := strings.Split(code, "\n")
	explicit := false
	unset := false
	for i, line := range lines {
		if !poolCtor.MatchString(line) {
			continue
		}
		if n := nearbyInt(lines, i, maxField, 30); n > 0 {
			explicit = true
			kind := "pg"
			if strings.Contains(code, "mysql") {
				kind = "mysql2"
				s.datastores["mysql"] = struct{}{}
			}
			s.pools = append(s.pools, poolHit{
				identity: kind + ":" + rel,
				kind:     kind,
				max:      n,
				profile:  "code",
				source:   fmt.Sprintf("%s:%d", rel, i+1),
			})
		} else {
			unset = true
		}
	}
	if unset && !explicit && (strings.Contains(code, "require('pg')") || strings.Contains(code, `require("pg")`) || strings.Contains(code, `from "pg"`) || strings.Contains(code, "from 'pg'")) {
		s.datastores["postgres"] = struct{}{}
		s.pools = append(s.pools, poolHit{
			identity: "pg-default:" + rel,
			kind:     "pg-default",
			max:      pgDefaultMax,
			profile:  "code",
			source:   rel + " pg default max 10",
		})
	}
	if explicit && (strings.Contains(code, "pg") || strings.Contains(code, "mysql")) {
		if strings.Contains(code, "mysql") {
			s.datastores["mysql"] = struct{}{}
		} else {
			s.datastores["postgres"] = struct{}{}
		}
	}
}

func (s *scanner) noteLoop(rel, original string) {
	lines := strings.Split(original, "\n")
	for i, line := range lines {
		if isComment(line) {
			continue
		}
		if !regexp.MustCompile(`^\s*(for|while)\b`).MatchString(line) && !strings.Contains(line, ".forEach(") {
			continue
		}
		base := indentOf(line)
		for j := i + 1; j < len(lines) && j < i+20; j++ {
			nxt := lines[j]
			if strings.TrimSpace(nxt) == "" || isComment(nxt) {
				continue
			}
			if indentOf(nxt) <= base && !strings.HasPrefix(strings.TrimSpace(nxt), ".") && !strings.HasPrefix(strings.TrimSpace(nxt), ")") && !strings.HasPrefix(strings.TrimSpace(nxt), "}") {
				break
			}
			if queryPattern.MatchString(nxt) {
				s.addLoc("query-in-loop", rel, j+1, "query")
				return
			}
		}
	}
}

func (s *scanner) noteFanout(rel, code string) {
	if fanoutPattern.MatchString(code) {
		s.addLoc("unbounded-fan-out", rel, lineOf(code, "Promise.all"), "Promise.all")
	}
}

func (s *scanner) noteHandlerConnection(rel, original string) {
	lines := strings.Split(original, "\n")
	handler := -1
	for i, line := range lines {
		if isComment(line) {
			continue
		}
		if handlerPattern.MatchString(line) || regexp.MustCompile(`\bhandleRequest\s*\(`).MatchString(line) {
			handler = i
			break
		}
	}
	if handler < 0 {
		return
	}
	base := indentOf(lines[handler])
	for i := handler + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" || isComment(line) {
			continue
		}
		if indentOf(line) <= base && i > handler+1 {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "class ") || strings.HasPrefix(trim, "function ") || strings.HasPrefix(trim, "def ") {
				break
			}
		}
		if regexp.MustCompile(`(getConnection\s*\(|createConnection\s*\(|mongoose\.connect\s*\()`).MatchString(line) ||
			(poolCtor.MatchString(line) && indentOf(line) > base) {
			s.addLoc("connection-per-invoke", rel, i+1, "connection")
			return
		}
	}
}

func (s *scanner) noteRetries(rel, code string) {
	if strings.Contains(code, "@Retryable") {
		if m := regexp.MustCompile(`maxAttempts\s*=\s*(\d+)`).FindStringSubmatch(code); m != nil {
			n, _ := strconv.Atoi(m[1])
			s.retries = append(s.retries, retry{Field: "max_attempts", Value: n, Source: rel})
		} else {
			s.retries = append(s.retries, retry{Field: "max_attempts", Value: 3, Source: rel + " @Retryable default"})
		}
	}
	if strings.Contains(code, "p-retry") || strings.Contains(code, "axios-retry") || strings.Contains(code, "async-retry") {
		if m := regexp.MustCompile(`\bretries\s*:\s*(\d+)`).FindStringSubmatch(code); m != nil {
			n, _ := strconv.Atoi(m[1])
			s.retries = append(s.retries, retry{Field: "retries", Value: n, Source: rel})
		}
	}
}

func (s *scanner) absorbAppConfig(rel, text, ext string) {
	docs := []string{text}
	if ext != ".properties" {
		docs = regexp.MustCompile(`\n---\s*\n`).Split(text, -1)
	}
	for _, doc := range docs {
		profile := profileOf(filepath.Base(rel), doc)
		parsed := map[string]string{}
		if ext == ".properties" {
			parsed = propertiesKeys(doc)
		} else {
			parsed = flatYAML(doc)
		}
		for key, raw := range parsed {
			s.keys = append(s.keys, cfgKey{key: key, raw: raw, profile: profile, file: rel})
			if kind := poolKind(key); kind != "" {
				if n := leadingInt(raw); n > 0 {
					s.pools = append(s.pools, poolHit{
						identity: kind + ":" + normKey(key),
						kind:     kind,
						max:      n,
						profile:  profile,
						source:   rel + " " + key,
					})
				}
			}
			blob := key + " " + raw
			if regexp.MustCompile(`(?i)jdbc:postgresql|postgres`).MatchString(blob) {
				s.datastores["postgres"] = struct{}{}
			}
			if regexp.MustCompile(`(?i)jdbc:mysql|mysql`).MatchString(blob) {
				s.datastores["mysql"] = struct{}{}
			}
			if strings.Contains(normKey(key), "spring.datasource") || strings.Contains(blob, "jdbc:") {
				s.datasource = true
			}
			if strings.Contains(normKey(key), "spring.jpa") || strings.Contains(normKey(key), "hibernate") {
				s.jpa = true
			}
		}
	}
}

func (s *scanner) absorbYAMLConfig(rel, profile, text string) {
	for key, raw := range flatYAML(text) {
		s.keys = append(s.keys, cfgKey{key: key, raw: raw, profile: profile, file: rel})
	}
}

func (s *scanner) absorbPom(rel, text string) {
	if strings.Contains(text, "spring-boot") || strings.Contains(text, "quarkus") || strings.Contains(text, "micronaut") {
		s.modules[filepath.Dir(rel)] = struct{}{}
	}
	for _, art := range regexp.MustCompile(`<artifactId>\s*([^<]+)\s*</artifactId>`).FindAllStringSubmatch(text, -1) {
		s.keepLibrary(strings.TrimSpace(art[1]))
	}
}

func (s *scanner) absorbGradle(rel, text string) {
	if strings.Contains(text, "spring-boot") || strings.Contains(text, "quarkus") || strings.Contains(text, "micronaut") {
		s.modules[filepath.Dir(rel)] = struct{}{}
	}
	s.keepLibrary(text)
}

func (s *scanner) absorbPackage(rel, text string) {
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if json.Unmarshal([]byte(text), &pkg) != nil {
		return
	}
	server := false
	for name := range pkg.Dependencies {
		s.keepLibrary(name)
		switch name {
		case "pg", "postgres":
			s.datastores["postgres"] = struct{}{}
			server = true
		case "mysql", "mysql2":
			s.datastores["mysql"] = struct{}{}
			server = true
		case "mongodb", "mongoose":
			s.datastores["mongo"] = struct{}{}
			server = true
		}
		if strings.Contains(name, "dynamodb") {
			s.datastores["dynamodb"] = struct{}{}
		}
		for _, marker := range []string{"express", "fastify", "koa", "@hono/", "@nestjs/core", "next"} {
			if strings.Contains(name, marker) {
				server = true
			}
		}
	}
	if server {
		s.modules[filepath.Dir(rel)] = struct{}{}
	}
}

func (s *scanner) keepLibrary(name string) {
	switch {
	case strings.Contains(name, "webflux"):
		s.libraries["spring-boot-starter-webflux"] = struct{}{}
		s.javaFlux = true
	case strings.Contains(name, "starter-web"):
		s.libraries["spring-boot-starter-web"] = struct{}{}
		s.javaMVC = true
	case strings.Contains(name, "data-jpa"), strings.Contains(name, "hibernate"):
		s.libraries["jpa"] = struct{}{}
		s.jpa = true
	case strings.Contains(name, "resilience4j"):
		s.libraries["resilience4j"] = struct{}{}
	case strings.Contains(name, "openfeign"):
		s.libraries["openfeign"] = struct{}{}
	case name == "opossum" || name == "cockatiel" || name == "p-retry" || name == "axios-retry" || name == "async-retry":
		s.libraries[name] = struct{}{}
	}
}

func (s *scanner) result() scanResult {
	if s.profile != "" {
		filtered := []cfgKey{}
		for _, k := range s.keys {
			if k.profile == "default" || k.profile == "code" || k.profile == s.profile {
				filtered = append(filtered, k)
			}
		}
		s.keys = filtered
	}

	if s.datasource && !hasPoolKind(s.pools, "hikari") {
		s.pools = append(s.pools, poolHit{
			identity: "hikari:default",
			kind:     "hikari",
			max:      hikariDefaultMax,
			profile:  "default",
			source:   "Hikari default max 10",
		})
	}
	modules := mapKeys(s.modules)
	multi := len(modules) > 1
	pools := s.pools
	if s.profile != "" {
		pools = nil
		for _, p := range s.pools {
			if p.profile == "code" || p.profile == "default" || p.profile == s.profile {
				pools = append(pools, p)
			}
		}
	}
	poolMax, poolSource := collapsePools(pools)
	if s.profile == "" {
		values := map[string]int{}
		for _, p := range pools {
			if n, ok := values[p.identity]; ok && n != p.max {
				poolMax = nil
				poolSource = ""
			}
			values[p.identity] = p.max
		}
	}
	if poolMax == nil && len(pools) > 0 {
		s.gaps = append(s.gaps, "conflicting pool configuration; resolve active profile and runtime overrides before using a pool budget")
	}
	breaker := s.breakerCode || breakerFromKeys(s.keys)
	feignTimeout := false
	var timeouts []named
	for _, item := range s.keys {
		n := normKey(item.key)
		if strings.Contains(n, "feign") && (strings.HasSuffix(n, "readtimeout") || strings.HasSuffix(n, "connecttimeout")) {
			feignTimeout = true
			timeouts = append(timeouts, named{Source: item.file + " " + item.key, Raw: item.raw})
		}
	}
	for i := range s.calls {
		configured := s.calls[i].TimeoutInFile
		if s.calls[i].Client == "FeignClient" && feignTimeout {
			configured = true
		}
		if s.calls[i].Client == "OkHttpClient" && !s.calls[i].TimeoutInFile {
			// OkHttp's builder defaults to a 10s read timeout. An explicit 0 disables it;
			// that still contains a timeout call and is TimeoutInFile.
			configured = true
		}
		s.calls[i].TimeoutConfigured = configured
		if !configured {
			s.addLoc("outbound-no-timeout", s.calls[i].File, s.calls[i].Line, s.calls[i].Client)
		}
	}
	s.noteOpenInView()
	s.noteCPUScale()
	if multi {
		poolMax = nil
		poolSource = ""
	}
	out := scanResult{
		Scope: "java/node heuristic", AppProfile: s.profile, Gaps: s.gaps,
		Languages:        mapKeys(s.languages),
		Platforms:        mapKeys(s.platforms),
		Modules:          modules,
		MultiModule:      multi,
		JavaModel:        javaModel(s.javaMVC, s.javaFlux),
		Datastores:       mapKeys(s.datastores),
		Libraries:        mapKeys(s.libraries),
		NodeProcesses:    nodeProcesses(s.nodeCluster, s.nodeWorkers),
		PoolMax:          poolMax,
		PoolMaxSource:    poolSource,
		PoolTimeoutRaw:   hikariTimeout(s.keys),
		ThreadPoolMax:    threadPool(s.keys),
		OutboundTimeouts: timeouts,
		Breaker:          breaker,
		Retry:            chooseRetry(s.retries, s.keys),
		ScalingIntent:    s.scaling,
		Calls:            dedupeCalls(s.calls),
		Suspects:         s.suspects(breaker),
	}
	if multi {
		out.PoolNote = "more than one service module; scan that directory before using a pool budget"
	}
	if out.Calls == nil {
		out.Calls = []call{}
	}
	if out.Suspects == nil {
		out.Suspects = []suspect{}
	}
	return out
}

func (s *scanner) suspects(breaker bool) []suspect {
	catalog := []struct {
		id      string
		fact    string
		confirm string
	}{
		{"transaction-spans-http", "A database transaction stays open across an outbound call.", "Read the method and anything it calls. Keep this when the connection is still checked out while HTTP is in flight."},
		{"open-in-view", "JPA open-in-view holds a JDBC connection for the whole request, and the request makes an outbound call.", "Keep this when spring.jpa.open-in-view is true or left at the Spring Boot default, and the request calls downstream before the session closes."},
		{"outbound-no-timeout", "No outbound timeout in the same file as the client.", "Read the client bean and its HTTP config. Keep this only if the timeout is still absent."},
		{"unbounded-cache", "An in-process cache has no max size. A TTL does not cap cardinality.", "Keep this when entries live for the process and nothing evicts by size. A map that dies with the request is not this."},
		{"unbounded-body", "A request or response body is read without a small cap.", "Keep this when it reads a request body or an upstream response. Drop a startup file read."},
		{"cpu-hot", "Password hashing or a similar CPU-bound call is in the service.", "Keep this when it runs on the request path."},
		{"cpu-scaling-for-io", "CPU scaling and I/O calls coexist; runtime evidence is needed to identify the limiting resource.", "Keep only when measured pool/trace waits explain latency and CPU has headroom; merely making I/O calls does not invalidate CPU scaling."},
		{"connection-per-invoke", "A connection or pool is opened inside the handler.", "Keep this only if each invoke opens a session instead of reusing a pool created outside the handler."},
		{"unbounded-thread-pool", "Cached thread pool grows with queued work.", "Keep this when the pool serves request work."},
		{"query-in-loop", "A loop body contains a repository or query call.", "Keep this when each iteration hits a database or a downstream service."},
		{"unbounded-fan-out", "Promise.all maps a collection into concurrent calls.", "Keep this when each item is a remote or database call and nothing caps the width."},
		{"parallel-stream", "parallelStream may run a call on every fork-join thread.", "Keep this when the stream body calls a database or a downstream service."},
	}
	var out []suspect
	for _, item := range catalog {
		spots := dedupeLocs(s.locs[item.id])
		if len(spots) == 0 {
			continue
		}
		shown := spots
		if len(shown) > 5 {
			shown = shown[:5]
		}
		out = append(out, suspect{
			ID: item.id, Fact: item.fact, Confirm: item.confirm,
			Locations: shown, Count: len(spots),
		})
	}
	chosen := chooseRetry(s.retries, s.keys)
	if len(s.calls) > 0 && !breaker {
		first := s.calls[0]
		out = append(out, suspect{
			ID: "no-breaker", Fact: "Outbound calls exist and no application circuit breaker was found.",
			Confirm:   "Search Resilience4j, opossum, and cockatiel, including config, before reporting it missing.",
			Locations: []location{{File: first.File, Line: first.Line, Client: first.Client}}, Count: 1,
		})
	}
	if chosen != nil && !breaker && len(s.calls) > 0 {
		out = append(out, suspect{
			ID: "retry-without-breaker", Fact: fmt.Sprintf("Retry field %s is %d and no application breaker was found.", chosen.Field, chosen.Value),
			Confirm:   "Keep this when those retries wrap a downstream call.",
			Locations: []location{{File: chosen.Source, Line: 1, Client: chosen.Field}}, Count: 1,
		})
	}
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func (s *scanner) noteTransaction(rel, original string) {
	lines := strings.Split(original, "\n")
	for i, line := range lines {
		if isComment(line) {
			continue
		}
		if !strings.Contains(line, "@Transactional") &&
			!strings.Contains(line, "TransactionTemplate") &&
			!strings.Contains(line, ".transaction(") &&
			!strings.Contains(line, "startTransaction(") {
			continue
		}
		end := i + 50
		if end > len(lines) {
			end = len(lines)
		}
		for j := i + 1; j < end; j++ {
			if isComment(lines[j]) {
				continue
			}
			if !outboundCall(lines[j]) {
				continue
			}
			s.addLoc("transaction-spans-http", rel, i+1, "transaction")
			return
		}
	}
	if classTransactional(lines) && fileHasOutbound(lines) {
		s.addLoc("transaction-spans-http", rel, lineOf(original, "@Transactional"), "class")
	}
}

func classTransactional(lines []string) bool {
	for i, line := range lines {
		if isComment(line) || !strings.Contains(line, "@Transactional") {
			continue
		}
		end := i + 6
		if end > len(lines) {
			end = len(lines)
		}
		for _, next := range lines[i:end] {
			trim := strings.TrimSpace(next)
			if strings.HasPrefix(trim, "class ") || strings.Contains(trim, " class ") {
				return true
			}
		}
	}
	return false
}

func fileHasOutbound(lines []string) bool {
	for _, line := range lines {
		if !isComment(line) && outboundCall(line) {
			return true
		}
	}
	return false
}

func (s *scanner) noteHotPath(rel, text string) {
	if cpuHot.MatchString(text) {
		s.addLoc("cpu-hot", rel, lineOf(text, cpuHot), "hash")
	}
	if strings.Contains(text, "readAllBytes(") {
		s.addLoc("unbounded-body", rel, lineOf(text, "readAllBytes("), "body")
		return
	}
	if strings.Contains(text, "toByteArray(") {
		s.addLoc("unbounded-body", rel, lineOf(text, "toByteArray("), "body")
		return
	}
	if oversizedBodyLimit(text) {
		s.addLoc("unbounded-body", rel, lineOf(text, "limit"), "body")
	}
}

func (s *scanner) noteCache(rel, original string) {
	lines := strings.Split(original, "\n")
	assign := regexp.MustCompile(`(?i)\b[\w.]*cache\w*\s*(?::[^=]+)?=\s*new\s+(?:Map|HashMap|ConcurrentHashMap)\b`)
	for i, line := range lines {
		if isComment(line) {
			continue
		}
		switch {
		case strings.Contains(line, "Caffeine.newBuilder(") || strings.Contains(line, "CacheBuilder.newBuilder("):
			if !windowHas(lines, i, 25, "maximumSize", "maximumWeight") {
				s.addLoc("unbounded-cache", rel, i+1, "builder")
				return
			}
		case strings.Contains(line, "new NodeCache("):
			if !windowHas(lines, i, 20, "maxKeys") {
				s.addLoc("unbounded-cache", rel, i+1, "NodeCache")
				return
			}
		case strings.Contains(line, "new LRUCache(") || strings.Contains(line, "new LRU("):
			if !windowHas(lines, i, 15, "max") {
				s.addLoc("unbounded-cache", rel, i+1, "lru")
				return
			}
		case assign.MatchString(line):
			s.addLoc("unbounded-cache", rel, i+1, "map")
			return
		}
	}
}

func (s *scanner) noteScaling(rel, text string) {
	signals := []struct{ needle, signal string }{
		{"scaleOnCpuUtilization", "cpu"},
		{"scale_on_cpu_utilization", "cpu"},
		{"scaleOnMemoryUtilization", "memory"},
		{"scale_on_memory_utilization", "memory"},
		{"scaleOnRequestCount", "request"},
		{"scale_on_request_count", "request"},
		{"scaleToTrackCustomMetric", "custom"},
		{"scale_to_track_custom_metric", "custom"},
	}
	seen := map[string]struct{}{}
	for _, sig := range signals {
		if !strings.Contains(text, sig.needle) {
			continue
		}
		if _, ok := seen[sig.signal]; ok {
			continue
		}
		seen[sig.signal] = struct{}{}
		s.scaling = append(s.scaling, scalingIntent{
			File: rel, Line: lineOf(text, sig.needle), Signal: sig.signal, Call: sig.needle,
		})
	}
}

func (s *scanner) noteOpenInView() {
	if !s.jpa || len(s.calls) == 0 {
		return
	}
	explicit := false
	enabled := true
	file := s.calls[0].File
	for _, item := range s.keys {
		if strings.HasSuffix(normKey(item.key), "openinview") {
			explicit = true
			enabled = !isFalse(item.raw)
			file = item.file
		}
	}
	if explicit && !enabled {
		return
	}
	label := "open-in-view default"
	if explicit {
		label = "open-in-view true"
	}
	s.addLoc("open-in-view", file, 1, label)
}

func (s *scanner) noteCPUScale() {
	waits := len(s.locs["transaction-spans-http"]) > 0 ||
		len(s.locs["open-in-view"]) > 0 ||
		len(s.calls) > 0 ||
		s.datasource
	if !waits {
		return
	}
	for _, intent := range s.scaling {
		if intent.Signal != "cpu" {
			continue
		}
		s.addLoc("cpu-scaling-for-io", intent.File, intent.Line, intent.Call)
	}
}

func oversizedBodyLimit(text string) bool {
	if regexp.MustCompile(`(?i)limit\s*:\s*['\"]?infinity`).MatchString(text) {
		return true
	}
	for _, match := range bodyLimit.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(match[1])
		switch strings.ToLower(match[2]) {
		case "gb":
			return true
		case "mb":
			if n >= 5 {
				return true
			}
		case "kb":
			if n >= 5*1024 {
				return true
			}
		default:
			if n >= 5*1024*1024 {
				return true
			}
		}
	}
	return false
}

func outboundCall(line string) bool {
	low := strings.ToLower(line)
	for _, needle := range []string{
		"resttemplate", "webclient", "feign", "okhttp", "httpclient",
		"axios", "fetch(", "got(", "undici",
		"getforobject", "postforobject", "postforentity", ".retrieve(",
	} {
		if strings.Contains(low, needle) {
			return true
		}
	}
	return false
}

func windowHas(lines []string, start, n int, needles ...string) bool {
	end := start + n
	if end > len(lines) {
		end = len(lines)
	}
	blob := strings.Join(lines[start:end], "\n")
	for _, needle := range needles {
		if strings.Contains(blob, needle) {
			return true
		}
	}
	return false
}

func (s *scanner) addLoc(id, file string, line int, client string) {
	s.locs[id] = append(s.locs[id], location{File: file, Line: line, Client: client})
}

func collapsePools(pools []poolHit) (*int, string) {
	chosen := map[string]poolHit{}
	for _, pool := range pools {
		if prev, ok := chosen[pool.identity]; ok {
			if prev.profile != pool.profile {
				if prev.profile == "default" {
					chosen[pool.identity] = pool
					continue
				}
				if pool.profile == "default" {
					continue
				}
			}
			if prev.max != pool.max {
				return nil, "conflicting pool candidates"
			}
			chosen[pool.identity] = prev
			continue
		}
		chosen[pool.identity] = pool
	}
	if len(chosen) == 0 {
		return nil, ""
	}
	parts := make([]poolHit, 0, len(chosen))
	for _, pool := range chosen {
		parts = append(parts, pool)
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].source < parts[j].source })
	total := 0
	sources := make([]string, 0, len(parts))
	for _, pool := range parts {
		total += pool.max
		sources = append(sources, pool.source)
	}
	return &total, strings.Join(sources, " + ")
}

func hasPoolKind(pools []poolHit, kind string) bool {
	for _, pool := range pools {
		if pool.kind == kind {
			return true
		}
	}
	return false
}

func poolKind(key string) string {
	n := normKey(key)
	switch {
	case strings.HasSuffix(n, "maximumpoolsize"):
		return "hikari"
	case strings.HasSuffix(n, "connectionlimit"):
		return "mysql2"
	default:
		return ""
	}
}

func breakerFromKeys(keys []cfgKey) bool {
	enabled := []bool{}
	policy := false
	for _, item := range keys {
		n := normKey(item.key)
		if !strings.Contains(n, "circuitbreaker") {
			continue
		}
		if strings.HasSuffix(n, "enabled") {
			enabled = append(enabled, !isFalse(item.raw))
			continue
		}
		policy = true
	}
	if len(enabled) > 0 && !slices.Contains(enabled, true) && !policy {
		return false
	}
	return policy || slices.Contains(enabled, true)
}

func chooseRetry(notes []retry, keys []cfgKey) *retry {
	var best *retry
	consider := func(item retry) {
		if best == nil || item.Value > best.Value {
			best = new(item)
		}
	}
	for _, note := range notes {
		consider(note)
	}
	for _, item := range keys {
		n := normKey(item.key)
		if strings.Contains(n, "retry") && strings.HasSuffix(n, "maxattempts") {
			if v := leadingInt(item.raw); v > 0 {
				consider(retry{Field: "max_attempts", Value: v, Source: item.file + " " + item.key})
			}
		}
	}
	return best
}

func hikariTimeout(keys []cfgKey) string {
	for _, item := range keys {
		n := normKey(item.key)
		if strings.Contains(n, "hikari") && strings.HasSuffix(n, "connectiontimeout") {
			return item.raw
		}
	}
	return ""
}

func threadPool(keys []cfgKey) *int {
	for _, item := range keys {
		n := normKey(item.key)
		if strings.HasSuffix(n, "threads.max") {
			if v := leadingInt(item.raw); v > 0 {
				return &v
			}
		}
	}
	return nil
}

func javaModel(mvc, flux bool) string {
	switch {
	case mvc && flux:
		return "mixed"
	case flux:
		return "webflux"
	case mvc:
		return "mvc"
	default:
		return "unknown"
	}
}

func nodeProcesses(cluster, workers bool) string {
	switch {
	case cluster:
		return "cluster"
	case workers:
		return "worker-threads"
	default:
		return "not-found"
	}
}

func flatYAML(text string) map[string]string {
	type frame struct {
		indent int
		key    string
	}
	var stack []frame
	found := map[string]string{}
	for _, raw := range strings.Split(text, "\n") {
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		indent := indentOf(raw)
		line := strings.TrimSpace(raw)
		line = strings.TrimPrefix(line, "- ")
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		if !regexp.MustCompile(`^[\w.-]+$`).MatchString(key) {
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parts := make([]string, 0, len(stack)+1)
		for _, item := range stack {
			parts = append(parts, item.key)
		}
		parts = append(parts, key)
		val = strings.Trim(strings.TrimSpace(strings.Split(val, " #")[0]), `"'`)
		if val != "" && val != "|" && val != ">" && val != "|-" && val != ">-" {
			found[strings.Join(parts, ".")] = val
		}
		stack = append(stack, frame{indent: indent, key: key})
	}
	return found
}

func propertiesKeys(text string) map[string]string {
	found := map[string]string{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			key, val, ok = strings.Cut(line, ":")
			if !ok {
				continue
			}
		}
		found[strings.TrimSpace(key)] = strings.TrimSpace(strings.Split(val, " #")[0])
	}
	return found
}

func profileOf(name, doc string) string {
	if m := regexp.MustCompile(`on-profile:\s*([\w,-]+)`).FindStringSubmatch(doc); m != nil {
		return m[1]
	}
	if m := regexp.MustCompile(`(?m)^profiles:\s*([\w,-]+)`).FindStringSubmatch(doc); m != nil {
		return m[1]
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if strings.HasPrefix(base, "application-") {
		return strings.TrimPrefix(base, "application-")
	}
	return "default"
}

func codeText(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if isComment(line) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func isComment(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "*")
}

func lineOf(text string, needle any) int {
	switch n := needle.(type) {
	case string:
		for i, line := range strings.Split(text, "\n") {
			if isComment(line) {
				continue
			}
			if strings.Contains(line, n) {
				return i + 1
			}
		}
	case *regexp.Regexp:
		for i, line := range strings.Split(text, "\n") {
			if isComment(line) {
				continue
			}
			if n.MatchString(line) {
				return i + 1
			}
		}
	}
	return 1
}

func nearbyInt(lines []string, start int, pattern *regexp.Regexp, window int) int {
	end := start + window
	if end > len(lines) {
		end = len(lines)
	}
	for _, line := range lines[start:end] {
		if m := pattern.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

func indentOf(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

func leadingInt(raw string) int {
	m := regexp.MustCompile(`^\s*(\d+)`).FindStringSubmatch(raw)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func normKey(key string) string {
	key = strings.ToLower(key)
	key = strings.ReplaceAll(key, "-", "")
	key = strings.ReplaceAll(key, "_", "")
	return key
}

func isFalse(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "false", "no", "off":
		return true
	default:
		return false
	}
}

func dedupeCalls(calls []call) []call {
	seen := map[string]struct{}{}
	var out []call
	for _, item := range calls {
		key := item.File + ":" + item.Client
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func dedupeLocs(spots []location) []location {
	seen := map[string]struct{}{}
	var out []location
	for _, spot := range spots {
		key := fmt.Sprintf("%s:%d:%s", spot.File, spot.Line, spot.Client)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, spot)
	}
	return out
}

func mapKeys(in map[string]struct{}) []string {
	out := slices.Sorted(maps.Keys(in))
	if out == nil {
		return []string{}
	}
	return out
}

func skipDir(name string) bool {
	_, ok := skipDirs[name]
	return ok
}

func skipFile(name string) bool {
	if strings.HasSuffix(name, "Test.java") || strings.HasSuffix(name, "Tests.java") {
		return true
	}
	return strings.Contains(name, ".test.") || strings.Contains(name, ".spec.")
}

func readText(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > maxFileBytes {
		return "", fmt.Errorf("file exceeds scan size limit")
	}
	buf, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil || bytes.Contains(buf, []byte{0}) {
		return "", err
	}
	return strings.TrimPrefix(string(buf), "\uFEFF"), nil
}
