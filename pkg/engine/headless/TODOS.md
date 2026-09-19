# 🗺️ Headless-Crawler Road-Map  
---

## 🥇  Core Improvements (High-impact / first passes)

- [ ] After clicking on elements there isn't enough wait time to reflect SPA navigation

- [ ] **Replace exact DOM hash with perceptual fingerprint**  
  - Stage 1: keep current SHA-256 on stripped DOM for cheap exact-match.  
  - Stage 2: compute 64-bit SimHash/MinHash over 3–4-word shingles of the stripped DOM; treat pages equal when Hamming distance ≤ 3 bits.  
  - Stage 3 (optional): if SimHash inconclusive, take a low-res screenshot and compare pHash/dHash.  
  - Store `ExactHash` & `FuzzyHash`; update graph comparison logic.

- [ ] **Robust “page ready” detector**  
  - Inject `MutationObserver` + `requestIdleCallback`.  
  - Resolve when:  
    * no DOM mutation for N ms **and**  
    * `location.href`/`history.length`/`<title>` stable.  
  - Wrap as `page.WaitForRouteChange()` and replace `WaitPageLoadHeuristics`.

- [ ] **Lazy-load / infinite-scroll support**  
  - Loop: `scrollBy(0, viewportHeight*0.9)` until `scrollHeight` stops growing.  
  - Fallback: IntersectionObserver on a sentinel div.

- [ ] **Capture all secondary resource navigations**  
  - Enable `FetchEnable` + `Network.*` events.  
  - Record: XHR/Fetch URLs, WebSocket, EventSource, Service-Worker scripts.  
  - Feed new, in-scope URLs into the crawl queue (dedup by host/path).

---

## 🥈  Mid-term Enhancements

- [ ] **Dynamic form-filling**  
  - Generate values based on `type`, `pattern`, `min`, `max`, `maxlength`, `required`.  
  - Pluggable `ValueProvider` interface for site-specific logic.  

- [ ] **Site adapters / hooks**  
  - Allow user-supplied Go or JS snippets per hostname (e.g. dismiss cookie wall, auto login, click “load more”).  

- [ ] **Concurrent tab execution**  
  - Worker pool consuming the action queue.  
  - Use multiple `rod.Page` instances (shared browser) – make `CrawlGraph` concurrency-safe.

- [ ] **Smart time-out & retry budgets**  
  - Adaptive timeout: first nav longer, later ones shorter; one automatic reload on stall.  

- [ ] **Viewport variants**  
  - Crawl again at typical mobile (390×844) & tablet (768×1024) sizes to reveal responsive content.

- [ ] **Memory & process recycling**  
  - Close background tabs after use.  
  - If Chrome RSS > threshold, restart browser (persist cookies if needed).

- [ ] **Anti-bot hardening**  
  - Spoof fonts, canvas & audio contexts.  
  - Rotate realistic UA strings, languages, `hardwareConcurrency`.  
  - Optional headful mode via XVFB to enable GPU paths.

---

## 🥉  Nice-to-have / Advanced

- [ ] **Export crawl sessions**  
  - HAR or WARC output in parallel to existing JSON.

- [ ] **JS coverage tracking**  
  - `Profiler.startPreciseCoverage` → know which scripts never executed.

- [ ] **Metrics & health**  
  - Prometheus counters (pages, active tabs, JS errors, nav errors).  
  - `/debug/pprof` enabled by default.

- [ ] **TLS / proxy flexibility**  
  - Accept custom CA bundle, client certs, upstream proxy rotation.

- [ ] **Sandboxing & security**  
  - Run Chrome under seccomp / user-namespaces or separate UID/GID automatically.

- [ ] **Graceful crash recovery**  
  - Detect `Page.crashed` / `Browser.disconnected`; re-spawn browser, resume queue.


----------------------------------

Claude OPUS info below:



## 🎯 Critical Bug Fixes & Edge Cases

- [ ] **Handle iframe content extraction**
  - Cross-origin iframe detection and flagging
  - Same-origin iframe DOM traversal
  - Nested iframe support (up to N levels)

- [ ] **WebComponent & Shadow DOM support**
  - Detect custom elements with shadow roots
  - Traverse open shadow DOMs for form/link discovery
  - Handle slot-based content projection

- [ ] **Multi-window/tab detection**
  - Track `window.open()` calls that bypass current hooks
  - Handle popup windows that close parent
  - Manage tab focus for proper event firing

## 🔐 Authentication & Session Management

- [ ] **Auth state detection**
  - Detect login/logout UI patterns
  - Monitor cookie changes for session tracking
  - Implement auth health checks between actions

- [ ] **Multi-step auth flows**
  - OAuth redirect handling
  - 2FA/MFA detection and waiting
  - SAML/SSO flow support

- [ ] **Session persistence**
  - Save/restore cookies between crawls
  - Handle JWT token refresh
  - Detect and handle session timeouts

## 🎪 Advanced Interaction Patterns

- [ ] **Complex UI interactions**
  - Drag & drop detection and execution
  - File upload with generated test files
  - Multi-select and combo-box handling
  - Date/time picker interaction

- [ ] **Keyboard navigation support**
  - Tab-order based discovery
  - Keyboard shortcut detection (Ctrl+K, etc.)
  - Access key enumeration

- [ ] **Touch/mobile gestures**
  - Swipe detection for mobile views
  - Long-press context menus
  - Pinch-to-zoom aware navigation

## 📊 Analytics & Monitoring

- [ ] **Performance metrics**
  - Page load time tracking
  - JavaScript execution overhead
  - Memory usage per page state
  - Network request waterfalls

- [ ] **Crawl quality metrics**
  - Code coverage per domain
  - Unique vs duplicate state ratio
  - Action success/failure rates
  - Depth distribution analysis

- [ ] **Error tracking**
  - JavaScript console error capture
  - Network error categorization
  - CSP violation logging
  - Failed action root cause analysis

## 🧠 Smart Crawling Features

- [ ] **ML-based duplicate detection**
  - Train model on visual similarity
  - Semantic HTML structure comparison
  - Learn site-specific patterns

- [ ] **Priority queue optimization**
  - High-value path prediction
  - Anomaly detection for interesting states
  - Dynamic depth adjustment based on yield

- [ ] **State space reduction**
  - Identify and prune redundant actions
  - Detect pagination patterns
  - Group similar forms (search variations)

## 🛡️ Security & Compliance

- [ ] **CAPTCHA handling**
  - Detection of common CAPTCHA providers
  - Integration points for solving services
  - Graceful degradation strategies

- [ ] **Rate limiting & politeness**
  - Per-domain request throttling
  - Respect robots.txt for headless
  - Adaptive delays based on response times

- [ ] **Privacy compliance**
  - PII detection in forms
  - GDPR banner interaction
  - Data retention policies

## 🔌 Integration Features

- [ ] **API extraction**
  - GraphQL query/mutation detection
  - REST endpoint parameter learning
  - WebSocket message format detection

- [ ] **Export formats**
  - OpenAPI spec generation from discoveries
  - Postman collection export
  - Burp Suite state file compatibility

- [ ] **Workflow recording**
  - Playwright/Puppeteer script generation
  - Selenium IDE format export
  - Custom DSL for replay

## 🚀 Performance Optimizations

- [ ] **Rendering optimizations**
  - Disable images/fonts for text-only analysis
  - Viewport-based lazy rendering
  - CPU throttling for battery saving

- [ ] **Caching layer**
  - DOM diff caching
  - Screenshot perceptual hashes
  - JavaScript execution results

- [ ] **Distributed crawling**
  - Work queue distribution
  - State synchronization protocol
  - Result aggregation pipeline

## 🔧 Developer Experience

- [ ] **Debug tooling**
  - Live crawl visualization
  - State graph explorer UI
  - Action replay debugger

- [ ] **Configuration management**
  - Per-site config profiles
  - A/B testing different strategies
  - Hot-reload of site adapters

- [ ] **Testing infrastructure**
  - Headless crawler unit tests
  - Integration tests with test sites
  - Regression detection suite


state.go is the crawler’s “state-manager”.  
Everything else in the headless package (browser wrappers, normalizer, graph, diagnostics) either feeds data into it or asks it to restore a known state.  
To make the crawler scalable, reliable and de-dupe friendly the file should be responsible for exactly three things:

1. Build a reproducible fingerprint (“state ID”) for the current page.  
2. Persist the surrounding metadata that we need to replay that state later.  
3. Provide deterministic, cheapest-first logic to get back to any recorded state.

Below is a complete design that meets those goals and leaves room for future TODOs.


────────────────────────────────────────────────────────────────────────────
1.   Fingerprint strategy (page → id)
────────────────────────────────────────────────────────────────────────────
A. Canonical DOM extraction  
   • Use the existing domNormalizer (strip scripts, styles, dynamic IDs etc.).  
   • Remove all transient event-attributes (`onclick`, `onmouseover`, …).  
   • Collapse whitespace → single space.

B. Two-tier hash  
   • ExactHash  = SHA-256(strippedDOM).  
   • FuzzyHash  = SimHash64(4-word shingles of strippedDOM).  
   • Treat states equal if  
        - ExactHash matches, or  
        - Hamming(FuzzyHash, other.FuzzyHash) ≤ 3 bits.  
   • Persist both; the graph layer deduplicates on (ExactHash || close-enough FuzzyHash).

C. Optional visual fallback  
   • If comparison is inconclusive (≥ 4 bit distance but DOM len < 1 MiB)  
     → low-res screenshot, pHash/dHash → same threshold logic.  
   • Executed lazily to avoid perf hit.

Resulting struct:

type PageState struct {
    ExactHash  string // always present
    FuzzyHash  uint64 // present if SimHash computed
    URL        string
    Title      string
    Depth      int
    StrippedDOM string
    NavigationAction *Action // edge that produced this state
    Timestamp  time.Time
}

–––– Advantages  
• SimHash makes minor DOM variations (ads, CSRF tokens) resolve to the same state, reducing graph size.  
• Screenshot hash catches SPA view switches that don’t touch the DOM tree much but look different.

────────────────────────────────────────────────────────────────────────────
2.   Metadata collection (page → PageState)
────────────────────────────────────────────────────────────────────────────
Algorithm newPageState(page, causingAction):

1. Grab `page.Info()`; bail out if URL is empty or about:blank.  
2. outerHTML := page.HTML().  
3. stripped := domNormalizer.Apply(outerHTML).  
4. Build PageState as above.  
5. Compute hashes as described.  
6. Diagnostics hook (save stripped DOM, screenshots, etc.).  
7. Return the fully populated PageState.

Edge cases handled:  
• Empty page → custom ErrEmptyPage (already present).  
• Non-deterministic DOM normalizer failure → bubbled up with context.

────────────────────────────────────────────────────────────────────────────
3.   Return-to-origin algorithm (current page, targetOriginID) → (pageID, error)
────────────────────────────────────────────────────────────────────────────
Keep the existing three-level approach but hard-code their priority and exit conditions.

Step 0   Fast-fail: if currentID == target → done.

Step 1   Element re-use  
   • If `action.Element` is non-nil, locate by XPath, ensure Visible & Interactable,  
     *plus* DOM equality check under the canonicalizer to avoid false positives.  
   • If match, return targetOriginID.

Step 2   Browser history  
   • page.GetNavigationHistory()  
   • Walk back until (url == origin.URL && title == origin.Title).  
   • Limit: max 10 steps to avoid long loops.  
   • After each back() call wait with WaitForRouteChange() (new detector described below).  
   • Recompute fingerprint; if equal (exact or fuzzy) → success.

Step 3   Graph shortest path  
   • crawlerGraph.ShortestPath(currentID, targetID).  
   • If unreachable, retry from emptyPageHash (fresh tab).  
   • Execute each Action; after each, WaitForRouteChange().  
   • After final step verify state (same equality logic as Step 2).  
   • Failure → ErrNoNavigationPossible.

Enhancements  
• Cache the computed “distance” between two states; next call can skip graph search.  
• Record statistics (#navigationBackSuccessByMethod) to tune the priority order.

────────────────────────────────────────────────────────────────────────────
4.   “Page ready” detector (WaitForRouteChange)
────────────────────────────────────────────────────────────────────────────
Replace the brittle WaitPageLoadHeuristics with:

Injected JS once per tab:

const idle = () => new Promise(res => {
    const done = () => { obs.disconnect(); res(); };
    let t;
    const reset = () => { clearTimeout(t); t = setTimeout(done, 300); };
    const obs = new MutationObserver(reset);
    obs.observe(document, {subtree: true, childList: true, attributes: true});
    reset();
});

window.__katanaReady = () => Promise.all([
    idle(),
    new Promise(r => requestIdleCallback(r, {timeout: 5000}))
]);

Go side:

func (p *BrowserPage) WaitForRouteChange() error {
    ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
    defer cancel()
    return rod.Try(func() {
        p.Eval(ctx, `await window.__katanaReady()`)
    })
}

Detects route changes, SPA navigations, AJAX content, infinite scroll “settling”, etc.

────────────────────────────────────────────────────────────────────────────
5.   Extensibility hooks
────────────────────────────────────────────────────────────────────────────
• FingerprintStrategy interface so users can plug in custom SimHash/Screenshot logic.  
• ValueProvider & SiteAdapter interfaces already planned can depend on PageState to decide actions.  
• Diagnostics sink gets PageState + serialized Action graph for offline visualizer.

────────────────────────────────────────────────────────────────────────────
6.   Migration plan
────────────────────────────────────────────────────────────────────────────
1. Stage 1 (quick): keep old sha256 flow, introduce struct fields & interface but stub SimHash.  
2. Stage 2: integrate open-source SimHash library, enable fuzzy comparator in graph.  
3. Stage 3: optional pHash path guarded by feature flag.  
4. Replace WaitPageLoadHeuristics with WaitForRouteChange().  
5. Add metrics around navigation-back success.

This architecture keeps state.go focused, removes hidden coupling, and sets up the crawler for future road-map items (concurrent tabs, adapters, ML dedup, etc.) while remaining incremental enough to merge in small PRs.