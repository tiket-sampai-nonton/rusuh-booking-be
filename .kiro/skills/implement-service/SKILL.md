---
name: implement-service
description: Use when implementing a microservice from tasks.md spec with automated task execution, test loops, and checkpoint-based resumability
---

# Implement Service

Automated service implementation from tasks.md with checkpointing for resume after logout.

**Announce at start:** "I'm using the implement-service skill to build {service} service."

## Overview

Execute all tasks from a service's tasks.md file autonomously:
1. Read task definitions
2. Execute each task (implement → test → retry on fail)
3. Checkpoint progress after each task
4. Resume from checkpoint if interrupted
5. Verify with make ci at end

**Core principle:** Continuous execution with checkpointing. Never pause between tasks unless blocked.

## When to Use

- User says "Implement {service} service"
- User says "Resume {service} service"
- Have a specs/{service}-service/tasks.md file

## Checkpoint System

Progress saved to specs/{service}-service/.progress.json:

```json
{
  "service": "user",
  "status": "IN_PROGRESS",
  "currentTask": 3,
  "completedTasks": [1, 2],
  "failedAttempts": 0,
  "lastCheckpoint": "2026-10-20T15:00:00Z",
  "notes": "Task 2 complete, starting Task 3"
}
```

**On resume:** Read checkpoint, skip completed tasks, continue from currentTask.

## The Process

### Step 0: Check for Resume

```
IF specs/{service}-service/.progress.json exists AND status != COMPLETE:
  Read checkpoint
  Announce: "Resuming {service} service from Task {n}"
  Skip completed tasks
ELSE:
  Start fresh from Task 1
```

### Step 1: Load Tasks

1. Read specs/{service}-service/tasks.md
2. Parse all tasks (Task 1, Task 2, etc.)
3. Create todo list for tracking
4. Read specs/{service}-service/requirements.md for context
5. Read specs/{service}-service/design.md for patterns

### Step 2: Execute Each Task

For each task, follow this loop:

```
┌─────────────────────────────────────────────────────────┐
│  TASK EXECUTION LOOP                                    │
├─────────────────────────────────────────────────────────┤
│  1. Read task's Acceptance Criteria                     │
│  2. Read task's Implementation Steps                    │
│  3. For each implementation step:                       │
│     └─► Create/modify the specified file                │
│  4. Run tests: go test -race ./...                      │
│     ├─► PASS → Continue to step 5                       │
│     └─► FAIL → Analyze error, fix, retry (max 3)        │
│  5. Run task's Verify command                           │
│  6. Write checkpoint (.progress.json)                   │
│  7. Mark task complete in todo list                     │
│  8. Next task                                           │
└─────────────────────────────────────────────────────────┘
```

### Step 3: Test Failure Loop

When tests fail:

```
attempt = 1
WHILE test fails AND attempt <= 3:
  1. Read error message carefully
  2. Identify root cause (use systematic-debugging skill if needed)
  3. Fix the specific issue
  4. Re-run tests
  5. attempt++

IF still failing after 3 attempts:
  Write checkpoint with status: "BLOCKED"
  Add blocker details to notes
  STOP and report to user
```

### Step 4: Write Checkpoint

After EACH task completion, write to specs/{service}-service/.progress.json:

```json
{
  "service": "{service}",
  "status": "IN_PROGRESS",
  "currentTask": {next_task_number},
  "completedTasks": [1, 2, ...],
  "failedAttempts": 0,
  "lastCheckpoint": "{ISO8601_timestamp}",
  "notes": "{brief description of what was done}"
}
```

### Step 5: Final Verification

After all tasks complete:

```bash
cd services/{service}
make build
make test
make lint
make ci
```

Update checkpoint: `"status": "COMPLETE"`

## Quick Reference

| Command | Action |
|---------|--------|
| "Implement user service" | Start fresh, execute all tasks |
| "Resume user service" | Continue from checkpoint |
| "Implement user service task 3" | Start from specific task |
| "Status user service" | Show checkpoint progress |

## Service Execution Order

```
Phase 1 (can parallel): user, queue
Phase 2 (after Phase 1): booking
Phase 3 (after Phase 2): payment, notification
Phase 4 (after all): e2e
```

## Red Flags

**Never:**
- Skip writing checkpoint after task completion
- Ignore test failures beyond 3 retries
- Proceed with BLOCKED status
- Modify files outside the service being implemented
- Skip the Verify step

**Always:**
- Read the task's Acceptance Criteria first
- Follow Implementation Steps in order
- Run tests after each file creation
- Write checkpoint before moving to next task
- Use TDD skill for complex logic

## Handling Common Issues

| Issue | Action |
|-------|--------|
| Test compile error | Fix syntax, re-run |
| Test assertion fail | Check AC, fix logic |
| Import cycle | Refactor dependencies |
| Missing dependency | Check go.mod, add if needed |
| Flaky test | Add retry, check race conditions |
| Timeout | Increase timeout or optimize |

## Integration with Other Skills

**Uses:**
- **test-driven-development** - For writing tests first
- **systematic-debugging** - When tests fail repeatedly
- **verification-before-completion** - Before marking task done

**After completion:**
- **finishing-a-development-branch** - Commit and PR

## Example Session

```
User: "Implement user service"

Agent: "I'm using the implement-service skill to build user service.

Reading specs/user-service/tasks.md...
No checkpoint found, starting fresh.

== Task 1: Request OTP ==
Reading Acceptance Criteria (8 items)...
Creating domain/dto/otp_request.go...
Creating domain/valueobject/email.go...
Creating port/outbound/otp_store.go...
[... more files ...]
Running: go test -race ./...
✓ PASS (15 tests)
Running verify: curl -X POST localhost:8085/api/v1/auth/otp/request
✓ Returns 202
Checkpoint saved: Task 1 complete

== Task 2: Verify OTP ==
[... continues ...]"
```

## Resume Example

```
User: "Resume user service"

Agent: "I'm using the implement-service skill to build user service.

Reading checkpoint: specs/user-service/.progress.json
Status: IN_PROGRESS
Completed: Tasks 1, 2, 3
Resuming from: Task 4 (Logout)

== Task 4: Logout ==
[... continues ...]"
```
