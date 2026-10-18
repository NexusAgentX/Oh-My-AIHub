package api

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/observe"
	"net/http"
	"strconv"
	"time"
)

func (a *app) analyzeHallChannel(w http.ResponseWriter, r *http.Request) {
	a.analyzeDirectory(w, r, false)
}
func (a *app) analyzeSupplier(w http.ResponseWriter, r *http.Request) { a.analyzeDirectory(w, r, true) }
func (a *app) analyzeDirectory(w http.ResponseWriter, r *http.Request, supplier bool) {
	q := r.URL.Query()
	// Public analysis accepts only quality dimensions. Reject unexpected selectors
	// rather than accidentally widening scope through the private call API.
	allowed := map[string]bool{"from": true, "to": true, "model": true, "format": true, "stream": true, "channel": true, "result": true, "status": true, "group_by": true, "bucket_seconds": true}
	for key, values := range q {
		if !allowed[key] || len(values) != 1 {
			writeInvalidQuery(w)
			return
		}
	}
	id := r.PathValue("channelID")
	if supplier {
		id = r.PathValue("supplierID")
	}
	if !uuidPattern.MatchString(id) {
		writeObserveError(w, observe.ErrNotFound)
		return
	}
	from, err := time.Parse(time.RFC3339, q.Get("from"))
	if err != nil {
		writeInvalidQuery(w)
		return
	}
	to, err := time.Parse(time.RFC3339, q.Get("to"))
	if err != nil {
		writeInvalidQuery(w)
		return
	}
	bucket := 0
	if q.Get("bucket_seconds") != "" {
		bucket, err = strconv.Atoi(q.Get("bucket_seconds"))
		if err != nil {
			writeInvalidQuery(w)
			return
		}
	}
	if q.Get("channel") != "" && !uuidPattern.MatchString(q.Get("channel")) {
		writeInvalidQuery(w)
		return
	}
	status := q.Get("status")
	if status != "" && status != "none" {
		code, e := strconv.Atoi(status)
		if e != nil || code < 100 || code > 599 || strconv.Itoa(code) != status {
			writeInvalidQuery(w)
			return
		}
	}
	// Production shares the contribution publication barrier with the catalog.
	// The store still independently enforces approved group/supplier visibility.
	if a.groupChannels != nil {
		runtime := a.groupChannels.Runtime
		if runtime == nil {
			writeError(w, 503, "market_publication_unavailable", "生效快照尚未发布，请稍后重试")
			return
		}
		if _, err := runtime.Current(); err != nil {
			writeError(w, 503, "market_publication_unavailable", "生效快照尚未发布，请稍后重试")
			return
		}
	}
	report, err := a.observe.DirectoryAnalysis(r.Context(), observe.DirectoryFilter{TargetID: id, Supplier: supplier, From: from, To: to, Model: q.Get("model"), Format: q.Get("format"), Stream: q.Get("stream"), Channel: q.Get("channel"), Result: q.Get("result"), Status: status, Group: q.Get("group_by"), Bucket: bucket})
	if err != nil {
		writeObserveError(w, err)
		return
	}
	writeJSON(w, 200, report)
}
