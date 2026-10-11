// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package metatest

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/crypto/bcrypt"
	"miniflux.app/v2/internal/api"
	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/database"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/storage"
	"miniflux.app/v2/internal/ui"
)

// Opt-in, loopback-only database. Each run owns a separate schema and never
// drops a database or touches public tables. No feeds or LLMs are fetched.
func TestMetadataEndToEnd(t *testing.T) {
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	t.Setenv("FETCHER_ALLOW_PRIVATE_NETWORKS", "1")
	options, parseErr := config.NewConfigParser().ParseEnvironmentVariables()
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	previousOptions := config.Opts
	config.Opts = options
	t.Cleanup(func() { config.Opts = previousOptions })
	dsn := os.Getenv("TEST_META_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_META_DATABASE_URL to an isolated local PostgreSQL database")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || !strings.HasSuffix(u.Path, "_test") {
		t.Fatal("metadata integration requires a loopback database ending in _test")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("meta_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(db); err != nil {
		t.Fatal("rerun", err)
	}
	if err = database.IsSchemaUpToDate(db); err != nil {
		t.Fatal(err)
	}
	s := storage.NewStorage(db)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("metadata-test"), bcrypt.MinCost)
	exec(`INSERT INTO users(id,username,password,entries_per_page,mark_read_on_view) VALUES(1,'alice',$1,2,false),(2,'bob',$1,2,false)`, string(hash))
	exec(`INSERT INTO categories(id,user_id,title) VALUES(1,1,'visible'),(2,1,'hidden'),(3,2,'other')`)
	exec(`UPDATE categories SET hide_globally=true WHERE id=2`)
	exec(`INSERT INTO feeds(id,user_id,category_id,title,feed_url,site_url) VALUES(1,1,1,'one','https://example.org/feed','https://example.org'),(2,1,2,'hidden','https://example.org/hidden','https://example.org'),(3,2,3,'other','https://example.org/other','https://example.org'),(4,1,1,'second','https://example.org/second','https://example.org')`)
	exec(`INSERT INTO entries(id,user_id,feed_id,hash,published_at,changed_at,title,url,content,author,tags,document_vectors) SELECT i,CASE WHEN i=9 THEN 2 ELSE 1 END,CASE WHEN i=8 THEN 2 WHEN i=9 THEN 3 WHEN i=10 THEN 4 ELSE 1 END,i::text,now()-i*interval '1 minute',now(),'Original article '||i,'https://example.org/'||i,'<p>Original content</p>','Publisher',ARRAY['React'],to_tsvector('Original article') FROM generate_series(1,10) i`)
	apiServer := httptest.NewServer(api.NewHandler(s, nil))
	defer apiServer.Close()
	call := func(user, method, path, body string, want int) []byte {
		t.Helper()
		r, err := http.NewRequest(method, apiServer.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.SetBasicAuth(user, "metadata-test")
		r.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d: %s", method, path, res.StatusCode, data)
		}
		return data
	}
	reg := func(user, client, name string) {
		call(user, "POST", "/v1/meta/register", fmt.Sprintf(`{"client_id":%q,"name":%q,"description":"Описание <script>alert(1)</script>"}`, client, name), 201)
	}
	reg("alice", "app_a", "rating")
	reg("alice", "app_a", "summary")
	reg("alice", "app_b", "rating")
	reg("bob", "app_a", "rating")
	call("bob", "PUT", "/v1/entries/9/meta/app_a/rating", `{"value":"8.5"}`, 204)
	canonical := call("alice", "POST", "/v1/meta/register", `{"client_id":"app_a","name":"rating","description":"changed"}`, 200)
	if strings.Contains(string(canonical), "changed") {
		t.Fatal("registration overwrote description")
	}
	call("alice", "POST", "/v1/meta/register", `{"client_id":"bad.client","name":"rating"}`, 400)
	call("alice", "DELETE", "/v1/meta/app_a/rating", "", 404)
	var definitions []model.MetaDefinition
	json.Unmarshal(call("bob", "GET", "/v1/meta", "", 200), &definitions)
	if len(definitions) != 1 {
		t.Fatal("registry isolation", definitions)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 8 {
		wg.Go(func() {
			_, _, err := s.RegisterMeta(1, model.MetaDefinition{ClientID: "concurrent", Name: "field"})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	ds, err := s.MetaRegistry(1, "concurrent")
	if err != nil || len(ds) != 1 {
		t.Fatal("concurrent registration", ds, err)
	}
	set := func(id int, value string) {
		body, _ := json.Marshal(map[string]string{"value": value})
		call("alice", "PUT", fmt.Sprintf("/v1/entries/%d/meta/app_a/rating", id), string(body), 204)
	}
	for _, id := range []int{1, 2, 3, 4, 8, 10} {
		set(id, "8.5")
	}
	set(5, "")
	set(6, "7.9")
	call("alice", "PATCH", "/v1/entries/1/meta/app_a", `{"summary":"<script>alert(1)</script>\nSecond line"}`, 204)
	call("alice", "PUT", "/v1/entries/1/meta/app_b/rating", `{"value":"recommended"}`, 204)
	call("alice", "PATCH", "/v1/entries/1/meta/app_a", `{"rating":"lost","unknown":"bad"}`, 400)
	for _, bad := range []string{`null`, `8.5`, `true`, `[]`, `{}`} {
		call("alice", "PUT", "/v1/entries/1/meta/app_a/rating", `{"value":`+bad+`}`, 400)
		call("alice", "PATCH", "/v1/entries/1/meta/app_a", `{"summary":"lost","rating":`+bad+`}`, 400)
	}
	call("alice", "PUT", "/v1/entries/9/meta/app_a/rating", `{"value":"bad"}`, 404)
	call("bob", "PUT", "/v1/entries/1/meta/app_a/rating", `{"value":"bad"}`, 404)
	call("alice", "PUT", "/v1/entries/1/meta/other/rating", `{"value":"bad"}`, 400)
	errs = make(chan error, 2)
	wg.Go(func() { errs <- s.WriteMeta(1, 1, "app_a", map[string]string{"rating": "8.5"}, "") })
	wg.Go(func() { errs <- s.WriteMeta(1, 1, "app_b", map[string]string{"rating": "recommended"}, "") })
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	entry := func(path string) map[string]any {
		var e map[string]any
		if err := json.Unmarshal(call("alice", "GET", path, "", 200), &e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	if _, ok := entry("/v1/entries/1")["meta"]; ok {
		t.Fatal("implicit projection")
	}
	m := entry("/v1/entries/1?meta=*")["meta"].(map[string]any)
	if len(m) != 3 || m["app_a.rating"] != "8.5" || m["app_b.rating"] != "recommended" || !strings.Contains(m["app_a.summary"].(string), "Second line") {
		t.Fatal("lost metadata", m)
	}
	m = entry("/v1/entries/7?meta=app_a.rating")["meta"].(map[string]any)
	if len(m) != 0 {
		t.Fatal(m)
	}
	m = entry("/v1/entries/1?meta=app_a.rating&meta=app_a.rating")["meta"].(map[string]any)
	if len(m) != 1 {
		t.Fatal(m)
	}
	call("alice", "GET", "/v1/entries?meta=unknown.field", "", 400)
	call("alice", "GET", "/v1/entries?filter=rating:8.*", "", 400)
	count := func(path string, want int) {
		t.Helper()
		var result struct {
			Total int `json:"total"`
		}
		json.Unmarshal(call("alice", "GET", path, "", 200), &result)
		if result.Total != want {
			t.Fatalf("%s total=%d want %d", path, result.Total, want)
		}
	}
	count("/v1/entries?filter=app_a.rating:8.*&meta=app_a.summary&limit=2", 6)
	count("/v1/entries?filter=app_a.rating:8.*&filter=app_a.rating:7.*", 7)
	count("/v1/entries?filter=app_a.rating:7.*&filter=app_b.rating:recommended", 2)
	count("/v1/entries?filter=app_a.rating:*", 7)
	count("/v1/entries?filter=app_a.rating:", 1)
	count("/v1/entries?meta=app_a.rating", 9)
	count("/v1/feeds/1/entries?filter=app_a.rating:8.*", 4)
	count("/v1/categories/1/entries?filter=app_a.rating:8.*", 5)
	count("/v1/entries/ids?filter=app_a.rating:8.*&meta=*&limit=2", 6)
	count("/v1/entries?filter=app_a.rating:8.*&tags=absent", 0)
	count("/v1/entries?filter=app_a.rating:8.*&search=absent", 0)
	for _, tc := range []struct {
		direction string
		want      []int64
	}{{"asc", []int64{10, 8}}, {"desc", []int64{1, 2}}} {
		var result struct {
			Entries []model.Entry `json:"entries"`
		}
		if err := json.Unmarshal(call("alice", "GET", "/v1/entries?filter=app_a.rating:8.*&order=published_at&direction="+tc.direction+"&limit=2", "", 200), &result); err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, e := range result.Entries {
			ids = append(ids, e.ID)
		}
		if !slices.Equal(ids, tc.want) {
			t.Fatal("metadata changed chronological ordering", ids, tc.want)
		}
	}
	for _, tc := range []struct {
		value, pattern string
		want           int
	}{
		{"7.0", "7.*", 1}, {"17.0", "7.*", 0}, {"xEND", "*END", 1}, {"axb", "a*b", 1}, {"100%_", "100%_", 1},
		{"100xx", "100%_", 0}, {"*", `\*`, 1}, {`a\b`, `a\\b`, 1}, {"Привет", "Пр*", 1}, {"https://example.org/a", "https://example.org/*", 1}, {"ABC", "abc", 0},
	} {
		set(7, tc.value)
		q := url.Values{"filter": {"app_a.rating:" + tc.pattern}, "after_entry_id": {"6"}, "before_entry_id": {"8"}}
		count("/v1/entries?"+q.Encode(), tc.want)
	}
	call("alice", "DELETE", "/v1/entries/7/meta/app_a/rating", "", 204)
	call("alice", "DELETE", "/v1/entries/7/meta/app_a/rating", "", 204)
	// Metadata does not alter source fields or history/incremental-sync timestamps.
	before, err := s.NewEntryQueryBuilder(1).WithEntryIDs(1).GetEntry()
	if err != nil {
		t.Fatal(err)
	}
	set(1, "8.5")
	after, err := s.NewEntryQueryBuilder(1).WithEntryIDs(1).GetEntry()
	if err != nil {
		t.Fatal(err)
	}
	if before.Title != after.Title || before.Content != after.Content || !before.ChangedAt.Equal(after.ChangedAt) {
		t.Fatal("metadata altered native fields")
	}
	if err = s.UpdateEntryTitleAndContent(before); err != nil {
		t.Fatal(err)
	}
	if err = s.ToggleStarred(1, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.SetEntriesStatus(1, []int64{1}, "read"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetEntriesStatus(1, []int64{1}, "unread"); err != nil {
		t.Fatal(err)
	}
	if len(entry("/v1/entries/1?meta=*")["meta"].(map[string]any)) != 3 {
		t.Fatal("native updates lost metadata")
	}
	// Scheduled and forced refreshes share RefreshFeedEntries; exercise both
	// update-existing modes with parser-like entries carrying no metadata.
	for _, update := range []bool{false, true, true} {
		fresh := *before
		fresh.Meta = nil
		fresh.Title = "Original refreshed article"
		if _, err := s.RefreshFeedEntries(1, 1, model.Entries{&fresh}, update); err != nil {
			t.Fatal(err)
		}
		if len(entry("/v1/entries/1?meta=*")["meta"].(map[string]any)) != 3 {
			t.Fatal("refresh lost metadata")
		}
	}
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html><head><title>Original</title></head><body><article><p>"+strings.Repeat("Publisher original article content. ", 30)+"</p></article></body></html>")
	}))
	defer original.Close()
	exec(`UPDATE entries SET url=$1 WHERE id=1`, original.URL)
	call("alice", "GET", "/v1/entries/1/fetch-content?update_content=true", "", 200)
	if len(entry("/v1/entries/1?meta=*")["meta"].(map[string]any)) != 3 {
		t.Fatal("original-content fetch lost metadata")
	}
	// Real web session, CSRF, escaped presentation and collection navigation.
	web := httptest.NewServer(ui.Serve(s, nil))
	defer web.Close()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	get := func(path string) string {
		t.Helper()
		res, err := browser.Get(web.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatalf("web %s: %d %s", path, res.StatusCode, b)
		}
		return string(b)
	}
	csrfPattern := regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	csrf := csrfPattern.FindStringSubmatch(get("/"))
	if len(csrf) != 2 {
		t.Fatal("login CSRF missing")
	}
	res, err := browser.PostForm(web.URL+"/login", url.Values{"username": {"alice"}, "password": {"metadata-test"}, "csrf": {csrf[1]}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	settings := get("/settings/meta")
	if strings.Contains(settings, "<script>alert(1)</script>") || !strings.Contains(settings, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("unsafe or missing registry description")
	}
	csrf = csrfPattern.FindStringSubmatch(settings)
	if len(csrf) != 2 {
		t.Fatal("settings CSRF missing", settings)
	}
	res, err = browser.PostForm(web.URL+"/settings/meta", url.Values{"csrf": {csrf[1]}, "filter_field": {"app_a.rating", "app_a.rating"}, "filter_pattern": {"8.*", "9.*"}, "title_field": {"app_b.rating", "app_a.rating"}, "content_field": {"app_a.summary"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	p, err := s.MetaPreferences(1)
	if err != nil || len(p.Filters) != 2 || len(p.TitleFields) != 2 || p.TitleFields[0] != "app_b.rating" {
		t.Fatal("preferences", p, err)
	}
	nav, err := s.GetNavMetadata(1)
	if err != nil || nav.CountUnread != 5 {
		t.Fatal("saved count", nav, err)
	}
	count("/v1/entries?status=unread", 9)
	raw := get("/unread")
	if !strings.Contains(raw, `class="unread-counter">8</span>`) {
		t.Fatal("raw unread inherited filters")
	}
	// App launches and homepage navigation must use the same saved query as
	// the Unread menu, even though the root and manifest routes are public.
	savedUnreadURL := "/unread?" + p.QueryValues().Encode()
	rawDoc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{`.logo a`, `a[data-page="unread"]`} {
		if href, _ := rawDoc.Find(selector).Attr("href"); href != savedUnreadURL {
			t.Fatalf("%s lost saved filters: %s", selector, href)
		}
	}
	noRedirect := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	checkHomeRedirect := func(res *http.Response, err error, want string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != want {
			t.Fatalf("homepage redirect: %d %s, want %s", res.StatusCode, res.Header.Get("Location"), want)
		}
	}
	res, err = noRedirect.Get(web.URL + "/")
	checkHomeRedirect(res, err, savedUnreadURL)
	for _, redirect := range []string{"", "/unread?filter=app_a.rating:7.*"} {
		res, err = noRedirect.PostForm(web.URL+"/login", url.Values{"username": {"alice"}, "password": {"metadata-test"}, "csrf": {csrf[1]}, "redirect_url": {redirect}})
		want := redirect
		if want == "" {
			want = savedUnreadURL
		}
		checkHomeRedirect(res, err, want)
	}
	var manifest struct {
		StartURL  string `json:"start_url"`
		Shortcuts []struct {
			URL string `json:"url"`
		} `json:"shortcuts"`
	}
	if err := json.Unmarshal([]byte(get("/manifest.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.StartURL != "/" || manifest.Shortcuts[1].URL != savedUnreadURL {
		t.Fatal("app launch/shortcut lost saved query", manifest)
	}
	exec(`UPDATE users SET default_home_page='starred' WHERE id=1`)
	res, err = noRedirect.Get(web.URL + "/")
	checkHomeRedirect(res, err, "/starred")
	starredDoc, err := goquery.NewDocumentFromReader(strings.NewReader(get("/starred")))
	if err != nil {
		t.Fatal(err)
	}
	if href, _ := starredDoc.Find(".logo a").Attr("href"); href != "/starred" {
		t.Fatal("non-Unread homepage changed", href)
	}
	exec(`UPDATE users SET default_home_page='unread' WHERE id=1`)
	filtered := get("/unread?filter=app_a.rating:8.*&meta=app_a.summary")
	if !strings.Contains(filtered, `class="unread-counter">5</span>`) {
		t.Fatal("filtered count missing", filtered)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(filtered))
	if err != nil {
		t.Fatal(err)
	}
	link, _ := doc.Find(`a[data-page="next"]`).First().Attr("href")
	if !strings.Contains(link, "filter=") || !strings.Contains(link, "meta=") {
		t.Fatal("pagination lost query", link)
	}
	call("alice", "PUT", "/v1/entries/1/meta/app_b/rating", `{"value":"<script>alert(1)</script>"}`, 204)
	article := get("/unread/entry/1?filter=app_a.rating:8.*")
	if strings.Contains(article, "<script>alert(1)</script>") || !strings.Contains(article, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("unsafe or missing metadata presentation")
	}
	articleDoc, _ := goquery.NewDocumentFromReader(strings.NewReader(article))
	if !strings.Contains(articleDoc.Find(".meta-title").Text(), "<script>alert(1)</script>") {
		t.Fatal("missing escaped title metadata")
	}
	call("alice", "PUT", "/v1/entries/1/meta/app_b/rating", `{"value":"recommended"}`, 204)
	articleDoc.Find(`a[rel="next"],a[rel="prev"]`).Each(func(_ int, selection *goquery.Selection) {
		href, _ := selection.Attr("href")
		if !strings.Contains(href, "filter=") {
			t.Fatal("article navigation lost query", href)
		}
	})
	shareCode, err := s.EntryShareCode(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	shared := get("/share/" + shareCode + "?meta=*")
	if strings.Contains(shared, "Second line") || strings.Contains(shared, "recommended") {
		t.Fatal("shared page leaked metadata")
	}
	for _, path := range []string{"/history", "/starred", "/feed/1/entries", "/feed/1/entries/all", "/category/1/entries", "/category/1/entries/all", "/category/1/entries/starred", "/tags/React/entries/all", "/search?q=Original"} {
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		get(path + separator + "filter=app_a.rating:8.*")
	}
	// Bulk requires CSRF, then updates all five matches across three pages.
	action, _ := doc.Find(`[data-url*="collection/mark-as-read"]`).First().Attr("data-url")
	res, err = browser.Post(web.URL+action, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal("bulk CSRF bypass", res.StatusCode)
	}
	req, _ := http.NewRequest("POST", web.URL+action, nil)
	req.Header.Set("X-Csrf-Token", csrf[1])
	res, err = browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("bulk: %d %s", res.StatusCode, body)
	}
	count("/v1/entries?status=unread&filter=app_a.rating:8.*", 1) // hidden category
	count("/v1/entries?status=unread", 4)                         // nonmatches and hidden match survive
	nav, err = s.GetNavMetadata(1)
	if err != nil || nav.CountUnread != 0 {
		t.Fatal("post-bulk badge", nav, err)
	}
	for _, tc := range []struct {
		scope, search string
		want          []int64
	}{
		{"/feed/1/entries", "", []int64{1, 2, 3, 4}},
		{"/category/1/entries/all", "", []int64{1, 2, 3, 4, 10}},
		{"/category/1/entries/starred", "", []int64{1}},
		{"/tags/React/entries/all", "", []int64{1, 2, 3, 4, 8, 10}},
		{"/search", "refreshed", []int64{1}},
		{"/starred", "", []int64{1}},
	} {
		exec(`UPDATE entries SET status='unread'`)
		query := url.Values{"scope": {tc.scope}, "filter": {"app_a.rating:8.*"}, "q": {tc.search}}
		req, _ := http.NewRequest("POST", web.URL+"/collection/mark-as-read?"+query.Encode(), nil)
		req.Header.Set("X-Csrf-Token", csrf[1])
		res, err := browser.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("bulk %s: %d %s", tc.scope, res.StatusCode, b)
		}
		ids, err := s.NewEntryQueryBuilder(1).WithStatuses("read").WithSorting("id", "asc").GetEntryIDs()
		if err != nil || !slices.Equal(ids, tc.want) {
			t.Fatalf("bulk %s: %v want %v, %v", tc.scope, ids, tc.want, err)
		}
	}
	// Deleting an assignment immediately changes the shortcut count without
	// removing its definition. Clearing the shortcut restores ordinary counts.
	exec(`UPDATE entries SET status='unread'`)
	call("alice", "DELETE", "/v1/entries/1/meta/app_a/rating", "", 204)
	nav, err = s.GetNavMetadata(1)
	if err != nil || nav.CountUnread != 4 {
		t.Fatal("assignment deletion badge", nav, err)
	}
	if err = s.SaveMetaPreferences(1, model.MetaPreferences{}); err != nil {
		t.Fatal(err)
	}
	nav, err = s.GetNavMetadata(1)
	if err != nil || nav.CountUnread != 8 {
		t.Fatal("cleared shortcut count", nav, err)
	}
	// Entry cleanup preserves definitions; user deletion cleans up normally.
	exec(`DELETE FROM entries WHERE id=1`)
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM entry_meta WHERE entry_id=1`).Scan(&n); err != nil || n != 0 {
		t.Fatal("entry cascade", n, err)
	}
	ds, err = s.MetaRegistry(1, "app_a")
	if err != nil || len(ds) != 2 {
		t.Fatal("registry lost on assignment cleanup")
	}
	// Exercise the real count on a synthetic NAS-sized collection, then inspect
	// the equivalent EXISTS shape. This is a regression fixture, not a latency SLA.
	exec(`INSERT INTO entries(id,user_id,feed_id,hash,published_at,changed_at,title,url,content,author) SELECT i,1,1,i::text,now(),now(),'Synthetic','https://example.org/'||i,'','' FROM generate_series(100,20099) i`)
	exec(`INSERT INTO entry_meta(entry_id,user_id,definition_id,value) SELECT e.id,1,r.id,CASE WHEN e.id%4=0 THEN '8.5' ELSE '3.0' END FROM entries e CROSS JOIN meta_registry r WHERE e.id>=100 AND r.user_id=1 AND r.client_id='app_a' AND r.name='rating'`)
	exec(`ANALYZE entries`)
	exec(`ANALYZE entry_meta`)
	exec(`ANALYZE meta_registry`)
	started := time.Now()
	n, err = s.NewEntryQueryBuilder(1).WithStatuses("unread").WithGloballyVisible().WithMetaQuery(model.MetaQuery{Filters: []model.MetaFilter{{Field: "app_a.rating", Pattern: "8.*"}, {Field: "app_b.rating", Pattern: "recommended"}}}).CountEntries()
	if err != nil || n != 5004 {
		t.Fatal("synthetic filtered count", n, err)
	}
	t.Logf("20,000-entry OR count: %s", time.Since(started))
	plan, err := db.Query(`EXPLAIN (ANALYZE,BUFFERS) SELECT count(*) FROM entries e JOIN feeds f ON f.id=e.feed_id JOIN categories c ON c.id=f.category_id WHERE e.user_id=1 AND e.status='unread' AND NOT f.hide_globally AND NOT c.hide_globally AND EXISTS (SELECT 1 FROM entry_meta m JOIN meta_registry r ON r.id=m.definition_id AND r.user_id=m.user_id WHERE m.entry_id=e.id AND m.user_id=e.user_id AND ((r.client_id||'.'||r.name='app_a.rating' AND m.value COLLATE "C" LIKE '8.%' ESCAPE '\') OR (r.client_id||'.'||r.name='app_b.rating' AND m.value COLLATE "C" LIKE 'recommended' ESCAPE '\')))`)
	if err != nil {
		t.Fatal(err)
	}
	for plan.Next() {
		var line string
		if err := plan.Scan(&line); err != nil {
			t.Fatal(err)
		}
		t.Log(line)
	}
	plan.Close()
	exec(`DELETE FROM users WHERE id=1`)
	if err = db.QueryRow(`SELECT count(*) FROM meta_registry WHERE user_id=1`).Scan(&n); err != nil || n != 0 {
		t.Fatal("user cascade", n, err)
	}
}
