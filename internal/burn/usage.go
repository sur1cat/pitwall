// Package claude reads Claude Code's local transcripts and turns the token
// usage recorded in them into hourly records. It is read-only apart from its
// own cache.
package burn

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/fnv"

	"github.com/sur1cat/pitwall/internal/claude"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Record is token usage for one hour, model, effort level and session.
type Record struct {
	Hour    time.Time `json:"hour"`
	Model   string    `json:"model"`
	Effort  string    `json:"effort"`
	Session string    `json:"session"`
	Project string    `json:"project"`
	Sub     bool      `json:"subagent"`
	// Branch is the git branch the work happened on, which is the closest
	// thing on disk to a unit of work: "what did this feature cost" has no
	// other answer.
	Branch   string `json:"branch,omitempty"`
	Messages int    `json:"messages"`
	Usage    Usage  `json:"usage"`
}

// Report is everything a scan found.
type Report struct {
	Records []Record
	// Duplicates counts API responses that appeared in more than one
	// transcript (session forks and resumes replay earlier messages).
	Duplicates int
	// Files and Cached count transcripts read and served from cache.
	Files, Cached int
	// Unknown lists models with no price, whose tokens are counted but not billed.
	Unknown []string
}

type bucket struct {
	Hour     int64  `json:"h"`
	Model    string `json:"m"`
	Effort   string `json:"e,omitempty"`
	Sub      bool   `json:"s,omitempty"`
	Branch   string `json:"b,omitempty"`
	Messages int    `json:"n"`
	In       int64  `json:"i,omitempty"`
	Out      int64  `json:"o,omitempty"`
	W5m      int64  `json:"w5,omitempty"`
	W1h      int64  `json:"w1,omitempty"`
	Read     int64  `json:"r,omitempty"`
}

type fileAgg struct {
	Size    int64    `json:"size"`
	ModTime int64    `json:"mtime"`
	Session string   `json:"session"`
	Project string   `json:"project"`
	First   int64    `json:"first"`
	Buckets []bucket `json:"buckets"`
	// IDs is a base64 blob of 64-bit hashes of every API response counted in
	// this file, used to detect messages replayed into another transcript.
	IDs string `json:"ids"`
}

// bucketKey is the grouping an hourly bucket is keyed by.
type bucketKey struct {
	hour   int64
	model  string
	effort string
	sub    bool
	branch string
}

type usageLine struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	Effort      string `json:"effort"`
	CWD         string `json:"cwd"`
	SessionID   string `json:"sessionId"`
	GitBranch   string `json:"gitBranch"`
	RequestID   string `json:"requestId"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			Input       int64 `json:"input_tokens"`
			Output      int64 `json:"output_tokens"`
			CacheCreate int64 `json:"cache_creation_input_tokens"`
			CacheRead   int64 `json:"cache_read_input_tokens"`
			Creation    struct {
				E5m int64 `json:"ephemeral_5m_input_tokens"`
				E1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

var (
	usageMarker = []byte(`"usage"`)
	asstMarker  = []byte(`"assistant"`)
)

// Scan reads every transcript and returns hourly usage records, deduplicated
// across transcripts.
func Scan(useCache bool) Report {
	var rep Report
	files := transcripts(filepath.Join(claude.Dir(), "projects"))
	if len(files) == 0 {
		return rep
	}
	rep.Files = len(files)

	cachePath := filepath.Join(claude.Dir(), "pitwall", "usage-cache.json")
	cache := map[string]fileAgg{}
	if useCache {
		if raw, err := os.ReadFile(cachePath); err == nil {
			_ = json.Unmarshal(raw, &cache)
		}
	}

	type job struct {
		path string
		agg  fileAgg
		hit  bool
	}
	jobs := make(chan string)
	done := make(chan job)
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				info, err := os.Stat(path)
				if err != nil {
					continue
				}
				if prev, ok := cache[path]; ok && prev.Size == info.Size() && prev.ModTime == info.ModTime().UnixNano() {
					done <- job{path: path, agg: prev, hit: true}
					continue
				}
				done <- job{path: path, agg: parse(path, info, nil)}
			}
		}()
	}
	go func() {
		for _, f := range files {
			jobs <- f
		}
		close(jobs)
		wg.Wait()
		close(done)
	}()

	fresh := make(map[string]fileAgg, len(files))
	paths := make([]string, 0, len(files))
	for j := range done {
		fresh[j.path] = j.agg
		paths = append(paths, j.path)
		if j.hit {
			rep.Cached++
		}
	}

	// Merge oldest transcript first so the original copy of a replayed
	// message is the one that gets counted.
	sort.Slice(paths, func(i, k int) bool {
		a, b := fresh[paths[i]], fresh[paths[k]]
		if a.First != b.First {
			return a.First < b.First
		}
		return paths[i] < paths[k]
	})

	seen := make(map[uint64]struct{}, 1<<16)
	unknown := map[string]bool{}
	for _, path := range paths {
		agg := fresh[path]
		ids := decodeIDs(agg.IDs)

		overlap := 0
		for _, id := range ids {
			if _, dup := seen[id]; dup {
				overlap++
			}
		}
		if overlap > 0 {
			// This transcript replays messages already counted elsewhere:
			// re-read it, skipping the duplicates.
			rep.Duplicates += overlap
			if info, err := os.Stat(path); err == nil {
				agg = parse(path, info, seen)
				ids = decodeIDs(agg.IDs)
			}
		}
		for _, id := range ids {
			seen[id] = struct{}{}
		}

		for _, b := range agg.Buckets {
			if !Known(b.Model) {
				unknown[b.Model] = true
			}
			rep.Records = append(rep.Records, Record{
				Hour:     time.Unix(b.Hour, 0),
				Model:    b.Model,
				Effort:   b.Effort,
				Session:  agg.Session,
				Project:  agg.Project,
				Sub:      b.Sub,
				Branch:   b.Branch,
				Messages: b.Messages,
				Usage: Usage{
					Input: b.In, Output: b.Out,
					CacheWrite5m: b.W5m, CacheWrite1h: b.W1h, CacheRead: b.Read,
				},
			})
		}
	}
	for m := range unknown {
		rep.Unknown = append(rep.Unknown, m)
	}
	sort.Strings(rep.Unknown)

	if useCache {
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
			if raw, err := json.Marshal(fresh); err == nil {
				_ = os.WriteFile(cachePath, raw, 0o644)
			}
		}
	}
	return rep
}

// parse reads one transcript into hourly buckets, skipping any API response
// whose hash is already in skip.
func parse(path string, info os.FileInfo, skip map[uint64]struct{}) fileAgg {
	agg := fileAgg{
		Size:    info.Size(),
		ModTime: info.ModTime().UnixNano(),
		Session: strings.TrimSuffix(filepath.Base(path), ".jsonl"),
	}
	f, err := os.Open(path)
	if err != nil {
		return agg
	}
	defer f.Close()

	buckets := map[bucketKey]*bucket{}
	local := map[uint64]struct{}{}
	var ids []uint64

	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && bytes.Contains(line, usageMarker) && bytes.Contains(line, asstMarker) {
			var e usageLine
			if json.Unmarshal(line, &e) == nil && e.Type == "assistant" {
				agg.absorb(e, buckets, local, skip, &ids)
			}
		}
		if err != nil {
			break
		}
	}

	for k, b := range buckets {
		b.Hour, b.Model, b.Effort, b.Sub, b.Branch = k.hour, k.model, k.effort, k.sub, k.branch
		agg.Buckets = append(agg.Buckets, *b)
	}
	sort.Slice(agg.Buckets, func(i, j int) bool { return agg.Buckets[i].Hour < agg.Buckets[j].Hour })
	if len(agg.Buckets) > 0 {
		agg.First = agg.Buckets[0].Hour
	}
	agg.IDs = encodeIDs(ids)
	return agg
}

func (agg *fileAgg) absorb(e usageLine, buckets map[bucketKey]*bucket, local, skip map[uint64]struct{}, ids *[]uint64) {
	model := Normalize(e.Message.Model)
	if model == "" {
		return
	}
	u := e.Message.Usage
	if u.Input == 0 && u.Output == 0 && u.CacheCreate == 0 && u.CacheRead == 0 {
		return
	}
	if agg.Project == "" && e.CWD != "" {
		agg.Project = filepath.Base(e.CWD)
	}

	id := hashID(e)
	if _, dup := local[id]; dup {
		return
	}
	if skip != nil {
		if _, dup := skip[id]; dup {
			return
		}
	}
	local[id] = struct{}{}
	*ids = append(*ids, id)

	ts, err := time.Parse(time.RFC3339, e.Timestamp)
	if err != nil {
		return
	}
	hour := ts.Truncate(time.Hour).Unix()

	w5, w1 := u.Creation.E5m, u.Creation.E1h
	if w5+w1 == 0 {
		w5 = u.CacheCreate // older records carry no TTL split
	}

	k := bucketKey{hour: hour, model: model, effort: e.Effort, sub: e.IsSidechain, branch: e.GitBranch}
	b := buckets[k]
	if b == nil {
		b = &bucket{}
		buckets[k] = b
	}
	b.Messages++
	b.In += u.Input
	b.Out += u.Output
	b.W5m += w5
	b.W1h += w1
	b.Read += u.CacheRead
}

// hashID identifies one API response. message.id is unique per response;
// requestId plus a timestamp is the fallback for older records.
func hashID(e usageLine) uint64 {
	h := fnv.New64a()
	if e.Message.ID != "" {
		_, _ = h.Write([]byte(e.Message.ID))
	} else {
		_, _ = h.Write([]byte(e.RequestID))
		_, _ = h.Write([]byte(e.Timestamp))
	}
	return h.Sum64()
}

func encodeIDs(ids []uint64) string {
	if len(ids) == 0 {
		return ""
	}
	raw := make([]byte, 8*len(ids))
	for i, id := range ids {
		binary.LittleEndian.PutUint64(raw[i*8:], id)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func decodeIDs(s string) []uint64 {
	if s == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	out := make([]uint64, 0, len(raw)/8)
	for i := 0; i+8 <= len(raw); i += 8 {
		out = append(out, binary.LittleEndian.Uint64(raw[i:]))
	}
	return out
}

func transcripts(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	return out
}
