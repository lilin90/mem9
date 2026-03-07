package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/qiffang/mnemos/server/internal/domain"
	"github.com/qiffang/mnemos/server/internal/embed"
	"github.com/qiffang/mnemos/server/internal/llm"
	"github.com/qiffang/mnemos/server/internal/repository"
	"github.com/qiffang/mnemos/server/internal/repository/tidb"
	"github.com/qiffang/mnemos/server/internal/tenant"
)

const uploadChunkSize = 50
const uploadMemoryBatchSize = 100
const defaultTaskTimeout = 30 * time.Minute

// SessionFile is the expected JSON format for session file uploads.
type SessionFile struct {
	AgentID   string          `json:"agent_id"`
	SessionID string          `json:"session_id"`
	Messages  []IngestMessage `json:"messages"`
}

// MemoryFile is the expected JSON format for memory file uploads.
type MemoryFile struct {
	AgentID  string            `json:"agent_id"`
	Memories []MemoryFileEntry `json:"memories"`
}

// MemoryFileEntry is a single memory entry in a memory file.
type MemoryFileEntry struct {
	Content    string         `json:"content"`
	Source     string         `json:"source,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	MemoryType string         `json:"memory_type,omitempty"`
}

// UploadWorker processes queued upload tasks.
type UploadWorker struct {
	tasks        repository.UploadTaskRepo
	tenants      repository.TenantRepo
	pool         *tenant.TenantPool
	embedder     *embed.Embedder
	llmClient    *llm.Client
	autoModel    string
	mode         IngestMode
	logger       *slog.Logger
	pollInterval time.Duration
}

// NewUploadWorker creates a new UploadWorker.
func NewUploadWorker(
	tasks repository.UploadTaskRepo,
	tenants repository.TenantRepo,
	pool *tenant.TenantPool,
	embedder *embed.Embedder,
	llmClient *llm.Client,
	autoModel string,
	mode IngestMode,
	logger *slog.Logger,
) *UploadWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &UploadWorker{
		tasks:        tasks,
		tenants:      tenants,
		pool:         pool,
		embedder:     embedder,
		llmClient:    llmClient,
		autoModel:    autoModel,
		mode:         mode,
		logger:       logger,
		pollInterval: 5 * time.Second,
	}
}

// Run starts the background worker loop.
func (w *UploadWorker) Run(ctx context.Context) error {
	logger := w.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("upload worker started")
	defer logger.Info("upload worker stopped")

	resetCount, err := w.tasks.ResetProcessing(ctx)
	if err != nil {
		return fmt.Errorf("reset processing tasks: %w", err)
	}
	if resetCount > 0 {
		logger.Info("reset processing upload tasks", "count", resetCount)
	}

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			tasks, err := w.tasks.FetchPending(ctx, 5)
			if err != nil {
				logger.Error("fetch pending upload tasks failed", "err", err)
				continue
			}
			if len(tasks) == 0 {
				continue
			}
			logger.Info("processing upload tasks", "count", len(tasks))
			for _, task := range tasks {
				if err := w.processTask(ctx, task); err != nil {
					logger.Error("task processing error", "task_id", task.TaskID, "err", err)
				}
			}
		}
	}
}

func (w *UploadWorker) processTask(ctx context.Context, task domain.UploadTask) error {
	logger := w.logger
	if logger == nil {
		logger = slog.Default()
	}

	// Per-task timeout to prevent indefinite blocking
	taskCtx, cancel := context.WithTimeout(ctx, defaultTaskTimeout)
	defer cancel()

	tenantInfo, err := w.tenants.GetByID(taskCtx, task.TenantID)
	if err != nil {
		w.cleanupFileOnFailure(task, logger)
		return w.failTask(taskCtx, task.TaskID, fmt.Errorf("resolve tenant: %w", err), logger)
	}

	db, err := w.pool.Get(taskCtx, tenantInfo.ID, tenantInfo.DSN())
	if err != nil {
		w.cleanupFileOnFailure(task, logger)
		return w.failTask(taskCtx, task.TaskID, fmt.Errorf("get tenant db: %w", err), logger)
	}

	memRepo := tidb.NewMemoryRepo(db, w.autoModel)
	ingestSvc := NewIngestService(memRepo, w.llmClient, w.embedder, w.autoModel, w.mode)

	data, err := os.ReadFile(task.FilePath)
	if err != nil {
		w.cleanupFileOnFailure(task, logger)
		return w.failTask(taskCtx, task.TaskID, fmt.Errorf("read upload file: %w", err), logger)
	}

	doneChunks := task.DoneChunks
	agentName := task.AgentID
	if agentName == "" {
		agentName = "upload-worker"
	}

	switch task.FileType {
	case domain.FileTypeSession:
		var file SessionFile
		if err := json.Unmarshal(data, &file); err != nil {
			w.cleanupFileOnFailure(task, logger)
			return w.failTask(taskCtx, task.TaskID, fmt.Errorf("parse session file: %w", err), logger)
		}
		if file.AgentID == "" {
			file.AgentID = task.AgentID
		}
		if file.SessionID == "" {
			file.SessionID = task.SessionID
		}

		chunks := chunkMessages(file.Messages, uploadChunkSize)
		// Handle empty file: mark done immediately
		if len(chunks) == 0 {
			if err := w.tasks.UpdateTotalChunks(taskCtx, task.TaskID, 0); err != nil {
				w.cleanupFileOnFailure(task, logger)
				return w.failTask(taskCtx, task.TaskID, fmt.Errorf("update total chunks: %w", err), logger)
			}
			// Empty file: skip to done
			break
		}

		// Set total_chunks after parsing so progress reporting works correctly.
		if err := w.tasks.UpdateTotalChunks(taskCtx, task.TaskID, len(chunks)); err != nil {
			w.cleanupFileOnFailure(task, logger)
			return w.failTask(taskCtx, task.TaskID, fmt.Errorf("update total chunks: %w", err), logger)
		}

		// Skip already-processed chunks (crash recovery)
		for i, chunk := range chunks {
			if i < doneChunks {
				continue // Already processed before crash
			}
			_, err := ingestSvc.Ingest(taskCtx, agentName, IngestRequest{
				AgentID:   file.AgentID,
				SessionID: file.SessionID,
				Messages:  chunk,
				Mode:      w.mode,
			})
			if err != nil {
				w.cleanupFileOnFailure(task, logger)
				return w.failTask(taskCtx, task.TaskID, fmt.Errorf("ingest session chunk: %w", err), logger)
			}
			doneChunks++
			if err := w.tasks.UpdateProgress(taskCtx, task.TaskID, doneChunks); err != nil {
				w.cleanupFileOnFailure(task, logger)
				return w.failTask(taskCtx, task.TaskID, fmt.Errorf("update progress: %w", err), logger)
			}
		}

	case domain.FileTypeMemory:
		var file MemoryFile
		if err := json.Unmarshal(data, &file); err != nil {
			w.cleanupFileOnFailure(task, logger)
			return w.failTask(taskCtx, task.TaskID, fmt.Errorf("parse memory file: %w", err), logger)
		}
		if file.AgentID == "" {
			file.AgentID = task.AgentID
		}

		// Handle empty file: mark done immediately
		if len(file.Memories) == 0 {
			if err := w.tasks.UpdateTotalChunks(taskCtx, task.TaskID, 0); err != nil {
				w.cleanupFileOnFailure(task, logger)
				return w.failTask(taskCtx, task.TaskID, fmt.Errorf("update total chunks: %w", err), logger)
			}
			// Empty file: skip to done
			break
		}

		// Set total_chunks after parsing so progress reporting works correctly.
		totalBatches := (len(file.Memories) + uploadMemoryBatchSize - 1) / uploadMemoryBatchSize
		if err := w.tasks.UpdateTotalChunks(taskCtx, task.TaskID, totalBatches); err != nil {
			w.cleanupFileOnFailure(task, logger)
			return w.failTask(taskCtx, task.TaskID, fmt.Errorf("update total chunks: %w", err), logger)
		}

		// Skip already-processed batches (crash recovery)
		batchIdx := 0
		for i := 0; i < len(file.Memories); i += uploadMemoryBatchSize {
			if batchIdx < doneChunks {
				batchIdx++
				continue // Already processed before crash
			}
			end := i + uploadMemoryBatchSize
			if end > len(file.Memories) {
				end = len(file.Memories)
			}
			batch := file.Memories[i:end]
			memories := make([]*domain.Memory, 0, len(batch))
			for _, entry := range batch {
				metadata, err := marshalMetadata(entry.Metadata)
				if err != nil {
					w.cleanupFileOnFailure(task, logger)
					return w.failTask(taskCtx, task.TaskID, fmt.Errorf("marshal memory metadata: %w", err), logger)
				}
				memType := domain.TypeInsight
				if entry.MemoryType != "" {
					memType = domain.MemoryType(entry.MemoryType)
				}
				memories = append(memories, &domain.Memory{
					ID:         uuid.New().String(),
					Content:    entry.Content,
					Source:     entry.Source,
					Tags:       entry.Tags,
					Metadata:   metadata,
					MemoryType: memType,
					AgentID:    file.AgentID,
					State:      domain.StateActive,
					Version:    1,
					UpdatedBy:  agentName,
				})
			}
			if err := memRepo.BulkCreate(taskCtx, memories); err != nil {
				w.cleanupFileOnFailure(task, logger)
				return w.failTask(taskCtx, task.TaskID, fmt.Errorf("bulk create memories: %w", err), logger)
			}
			batchIdx++
			doneChunks++
			if err := w.tasks.UpdateProgress(taskCtx, task.TaskID, doneChunks); err != nil {
				w.cleanupFileOnFailure(task, logger)
				return w.failTask(taskCtx, task.TaskID, fmt.Errorf("update progress: %w", err), logger)
			}
		}

	default:
		w.cleanupFileOnFailure(task, logger)
		return w.failTask(taskCtx, task.TaskID, fmt.Errorf("unsupported file type %q", task.FileType), logger)
	}
	if err := w.tasks.UpdateStatus(taskCtx, task.TaskID, domain.TaskDone, ""); err != nil {
		// Task succeeded but finalization failed - do NOT delete file so retry is possible
		logger.Error("task completed but status update failed - file retained for retry", "task_id", task.TaskID, "err", err)
		return fmt.Errorf("update task status done: %w", err)
	}

	// Only delete file after successful finalization
	w.cleanupFileOnSuccess(task, logger)
	logger.Info("upload task completed", "task_id", task.TaskID)
	return nil

}

func (w *UploadWorker) failTask(ctx context.Context, taskID string, err error, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if updateErr := w.tasks.UpdateStatus(ctx, taskID, domain.TaskFailed, err.Error()); updateErr != nil {
		logger.Error("failed to update upload task status", "task_id", taskID, "err", updateErr)
	}
	logger.Error("upload task failed", "task_id", taskID, "err", err)
	return err
}

// cleanupFileOnSuccess removes the file after task completes successfully.
func (w *UploadWorker) cleanupFileOnSuccess(task domain.UploadTask, logger *slog.Logger) {
	if task.FilePath == "" {
		return
	}
	if err := os.Remove(task.FilePath); err != nil && !os.IsNotExist(err) {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Error("failed to remove upload file after success", "task_id", task.TaskID, "path", task.FilePath, "err", err)
	}
}

// cleanupFileOnFailure removes the file when task fails permanently.
func (w *UploadWorker) cleanupFileOnFailure(task domain.UploadTask, logger *slog.Logger) {
	if task.FilePath == "" {
		return
	}
	if err := os.Remove(task.FilePath); err != nil && !os.IsNotExist(err) {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Error("failed to remove upload file after failure", "task_id", task.TaskID, "path", task.FilePath, "err", err)
	}
}

func marshalMetadata(metadata map[string]any) (json.RawMessage, error) {
	if metadata == nil {
		return nil, nil
	}
	b, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

func chunkMessages(msgs []IngestMessage, size int) [][]IngestMessage {
	if size <= 0 {
		if len(msgs) == 0 {
			return nil
		}
		return [][]IngestMessage{msgs}
	}
	chunks := make([][]IngestMessage, 0, (len(msgs)+size-1)/size)
	for i := 0; i < len(msgs); i += size {
		end := i + size
		if end > len(msgs) {
			end = len(msgs)
		}
		chunks = append(chunks, msgs[i:end])
	}
	return chunks
}
