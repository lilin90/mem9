package service

import (
	"context"
	"errors"
	"testing"

	"github.com/qiffang/mnemos/server/internal/domain"
)

type memoryRepoMock struct {
	createCalls []*domain.Memory
}

func (m *memoryRepoMock) Create(ctx context.Context, mem *domain.Memory) error {
	m.createCalls = append(m.createCalls, mem)
	return nil
}

func (m *memoryRepoMock) GetByID(ctx context.Context, id string) (*domain.Memory, error) {
	return nil, domain.ErrNotFound
}

func (m *memoryRepoMock) UpdateOptimistic(ctx context.Context, mem *domain.Memory, expectedVersion int) error {
	return nil
}

func (m *memoryRepoMock) SoftDelete(ctx context.Context, id, agentName string) error {
	return nil
}

func (m *memoryRepoMock) ArchiveMemory(ctx context.Context, id, supersededBy string) error {
	return nil
}

func (m *memoryRepoMock) SetState(ctx context.Context, id string, state domain.MemoryState) error {
	return nil
}

func (m *memoryRepoMock) List(ctx context.Context, f domain.MemoryFilter) ([]domain.Memory, int, error) {
	return nil, 0, nil
}

func (m *memoryRepoMock) Count(ctx context.Context) (int, error) {
	return 0, nil
}

func (m *memoryRepoMock) BulkCreate(ctx context.Context, memories []*domain.Memory) error {
	return nil
}

func (m *memoryRepoMock) VectorSearch(ctx context.Context, queryVec []float32, f domain.MemoryFilter, limit int) ([]domain.Memory, error) {
	return nil, nil
}

func (m *memoryRepoMock) AutoVectorSearch(ctx context.Context, queryText string, f domain.MemoryFilter, limit int) ([]domain.Memory, error) {
	return nil, nil
}

func (m *memoryRepoMock) KeywordSearch(ctx context.Context, query string, f domain.MemoryFilter, limit int) ([]domain.Memory, error) {
	return nil, nil
}

func (m *memoryRepoMock) FTSSearch(ctx context.Context, query string, f domain.MemoryFilter, limit int) ([]domain.Memory, error) {
	return nil, nil
}

func (m *memoryRepoMock) ListBootstrap(ctx context.Context, limit int) ([]domain.Memory, error) {
	return nil, nil
}

func TestStripInjectedContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []IngestMessage
		expected []IngestMessage
	}{
		{
			name: "removes relevant memories tag",
			input: []IngestMessage{{
				Role:    "user",
				Content: "keep <relevant-memories>remove</relevant-memories> text",
			}},
			expected: []IngestMessage{{Role: "user", Content: "keep  text"}},
		},
		{
			name: "handles no tags",
			input: []IngestMessage{{
				Role:    "assistant",
				Content: "no tags here",
			}},
			expected: []IngestMessage{{Role: "assistant", Content: "no tags here"}},
		},
		{
			name: "handles malformed tag",
			input: []IngestMessage{{
				Role:    "user",
				Content: "keep <relevant-memories>broken",
			}},
			expected: []IngestMessage{{Role: "user", Content: "keep"}},
		},
		{
			name: "drops empty content",
			input: []IngestMessage{{
				Role:    "system",
				Content: "<relevant-memories>only</relevant-memories>",
			}},
			expected: []IngestMessage{},
		},
		{
			name: "handles multiple tags",
			input: []IngestMessage{{
				Role:    "user",
				Content: "a<relevant-memories>x</relevant-memories>b<relevant-memories>y</relevant-memories>c",
			}},
			expected: []IngestMessage{{Role: "user", Content: "abc"}},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stripInjectedContext(tt.input)
			if len(got) != len(tt.expected) {
				t.Fatalf("stripInjectedContext() len = %d, expected %d; got %#v", len(got), len(tt.expected), got)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Fatalf("stripInjectedContext()[%d] = %#v, expected %#v", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestStripMemoryTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single tag",
			input:    "a<relevant-memories>b</relevant-memories>c",
			expected: "ac",
		},
		{
			name:     "multiple tags",
			input:    "a<relevant-memories>b</relevant-memories>c<relevant-memories>d</relevant-memories>e",
			expected: "ace",
		},
		{
			name:     "malformed tag",
			input:    "prefix<relevant-memories>broken",
			expected: "prefix",
		},
		{
			name:     "nested tags",
			input:    "a<relevant-memories>one<relevant-memories>two</relevant-memories>three</relevant-memories>b",
			expected: "athree</relevant-memories>b",
		},
		{
			name:     "no tags",
			input:    "plain text",
			expected: "plain text",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stripMemoryTags(tt.input)
			if got != tt.expected {
				t.Fatalf("stripMemoryTags() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestFormatConversation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []IngestMessage
		expected string
	}{
		{
			name: "formats role content pairs",
			input: []IngestMessage{{
				Role:    "user",
				Content: "hi",
			}, {
				Role:    "assistant",
				Content: "hello",
			}},
			expected: "User: hi\n\nAssistant: hello",
		},
		{
			name:     "handles empty messages",
			input:    nil,
			expected: "",
		},
		{
			name: "capitalizes first letter only",
			input: []IngestMessage{{
				Role:    "uSER",
				Content: "case",
			}},
			expected: "USER: case",
		},
		{
			name: "trims trailing whitespace",
			input: []IngestMessage{{
				Role:    "user",
				Content: "trail",
			}},
			expected: "User: trail",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatConversation(tt.input)
			if got != tt.expected {
				t.Fatalf("formatConversation() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestParseIntID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{name: "valid integer", input: "42", expected: 42},
		{name: "negative integer", input: "-7", expected: -7},
		{name: "invalid string", input: "abc", expected: -1},
		{name: "empty string", input: "", expected: -1},
		{name: "trailing text", input: "12x", expected: 12},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseIntID(tt.input)
			if got != tt.expected {
				t.Fatalf("parseIntID() = %d, expected %d", got, tt.expected)
			}
		})
	}
}

func TestIngestEmptyMessages(t *testing.T) {
	t.Parallel()

	svc := NewIngestService(&memoryRepoMock{}, nil, nil, "", ModeSmart)
	_, err := svc.Ingest(context.Background(), "agent-1", IngestRequest{})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	var vErr *domain.ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
	if vErr.Field != "messages" {
		t.Fatalf("expected field 'messages', got %q", vErr.Field)
	}
}

func TestIngestModeRawStoresDigest(t *testing.T) {
	t.Parallel()

	memRepo := &memoryRepoMock{}
	svc := NewIngestService(memRepo, nil, nil, "", ModeSmart)

	req := IngestRequest{
		Mode:      ModeRaw,
		SessionID: "session-1",
		AgentID:   "agent-1",
		Messages: []IngestMessage{{
			Role:    "user",
			Content: "hello",
		}, {
			Role:    "assistant",
			Content: "world",
		}},
	}

	res, err := svc.Ingest(context.Background(), "agent-1", req)
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	if res == nil || !res.DigestStored {
		t.Fatalf("expected digest stored, got %#v", res)
	}
	if len(memRepo.createCalls) != 1 {
		t.Fatalf("expected 1 Create call, got %d", len(memRepo.createCalls))
	}

	created := memRepo.createCalls[0]
	expectedContent := "user: hello\n\nassistant: world"
	if created.Content != expectedContent {
		t.Fatalf("unexpected content: %q", created.Content)
	}
	if created.MemoryType != domain.TypeDigest {
		t.Fatalf("expected memory type digest, got %q", created.MemoryType)
	}
}

func TestIngestNilLLMFallsBackToRaw(t *testing.T) {
	t.Parallel()

	memRepo := &memoryRepoMock{}
	svc := NewIngestService(memRepo, nil, nil, "", ModeSmart)

	req := IngestRequest{
		Mode:      ModeSmart,
		SessionID: "session-2",
		AgentID:   "agent-2",
		Messages: []IngestMessage{{
			Role:    "user",
			Content: "hello",
		}},
	}

	res, err := svc.Ingest(context.Background(), "agent-2", req)
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	if res == nil || !res.DigestStored {
		t.Fatalf("expected digest stored, got %#v", res)
	}
	if len(memRepo.createCalls) != 1 {
		t.Fatalf("expected 1 Create call, got %d", len(memRepo.createCalls))
	}
}
