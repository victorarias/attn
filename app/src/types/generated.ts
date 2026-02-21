// To parse this data:
//
//   import { Convert, AddCommentMessage, AddCommentResultMessage, ApprovePRMessage, AttachResultMessage, AttachSessionMessage, AuthorState, AuthorsUpdatedMessage, Branch, BranchChangedMessage, BranchDiffFile, BranchDiffFilesResultMessage, BranchesResultMessage, CancelReviewMessage, CheckAttnStashMessage, CheckAttnStashResultMessage, CheckDirtyMessage, CheckDirtyResultMessage, ClearSessionsMessage, ClearWarningsMessage, CollapseRepoMessage, CommandErrorMessage, CommitWIPMessage, CommitWIPResultMessage, CreateBranchMessage, CreateBranchResultMessage, CreateWorktreeFromBranchMessage, CreateWorktreeMessage, CreateWorktreeResultMessage, DaemonWarning, DeleteBranchMessage, DeleteBranchResultMessage, DeleteCommentMessage, DeleteCommentResultMessage, DeleteWorktreeMessage, DeleteWorktreeResultMessage, DetachSessionMessage, EnsureRepoMessage, EnsureRepoResultMessage, FetchPRDetailsMessage, FetchPRDetailsResultMessage, FetchRemotesMessage, FetchRemotesResultMessage, FileDiffResultMessage, GetBranchDiffFilesMessage, GetCommentsMessage, GetCommentsResultMessage, GetDefaultBranchMessage, GetDefaultBranchResultMessage, GetFileDiffMessage, GetRecentLocationsMessage, GetRepoInfoMessage, GetRepoInfoResultMessage, GetReviewStateMessage, GetReviewStateResultMessage, GetSettingsMessage, GitFileChange, GitStatusUpdateMessage, HeartbeatMessage, HeatState, InitialStateMessage, InjectTestPRMessage, InjectTestSessionMessage, KillSessionMessage, ListBranchesMessage, ListRemoteBranchesMessage, ListRemoteBranchesResultMessage, ListWorktreesMessage, MarkFileViewedMessage, MarkFileViewedResultMessage, MergePRMessage, MuteAuthorMessage, MuteMessage, MutePRMessage, MuteRepoMessage, PR, PRActionResultMessage, PRRole, PRVisitedMessage, PRsUpdatedMessage, PtyDesyncMessage, PtyInputMessage, PtyOutputMessage, PtyResizeMessage, QueryAuthorsMessage, QueryMessage, QueryPRsMessage, QueryReposMessage, RateLimitedMessage, RecentLocation, RecentLocationsResultMessage, RefreshPRsMessage, RefreshPRsResultMessage, RegisterMessage, RepoInfo, RepoState, ReposUpdatedMessage, ResolveCommentMessage, ResolveCommentResultMessage, Response, ReviewCancelledMessage, ReviewChunkMessage, ReviewComment, ReviewCommentResolvedMessage, ReviewCompleteMessage, ReviewFinding, ReviewFindingMessage, ReviewStartedMessage, ReviewState, ReviewToolUse, ReviewToolUseMessage, Session, SessionAgent, SessionExitedMessage, SessionRegisteredMessage, SessionState, SessionStateChangedMessage, SessionTodosUpdatedMessage, SessionUnregisteredMessage, SessionVisualizedMessage, SessionsUpdatedMessage, SetSettingMessage, SettingsUpdatedMessage, SpawnResultMessage, SpawnSessionMessage, StartReviewMessage, StashMessage, StashPopMessage, StashPopResultMessage, StashResultMessage, StateMessage, StopMessage, SubscribeGitStatusMessage, SwitchBranchMessage, SwitchBranchResultMessage, TodosMessage, UnregisterMessage, UnsubscribeGitStatusMessage, UpdateCommentMessage, UpdateCommentResultMessage, WebSocketEvent, WontFixCommentMessage, WontFixCommentResultMessage, Worktree, WorktreeCreatedEvent, WorktreeDeletedEvent, WorktreesUpdatedMessage } from "./file";
//
//   const addCommentMessage = Convert.toAddCommentMessage(json);
//   const addCommentResultMessage = Convert.toAddCommentResultMessage(json);
//   const approvePRMessage = Convert.toApprovePRMessage(json);
//   const attachResultMessage = Convert.toAttachResultMessage(json);
//   const attachSessionMessage = Convert.toAttachSessionMessage(json);
//   const authorState = Convert.toAuthorState(json);
//   const authorsUpdatedMessage = Convert.toAuthorsUpdatedMessage(json);
//   const branch = Convert.toBranch(json);
//   const branchChangedMessage = Convert.toBranchChangedMessage(json);
//   const branchDiffFile = Convert.toBranchDiffFile(json);
//   const branchDiffFilesResultMessage = Convert.toBranchDiffFilesResultMessage(json);
//   const branchesResultMessage = Convert.toBranchesResultMessage(json);
//   const cancelReviewMessage = Convert.toCancelReviewMessage(json);
//   const checkAttnStashMessage = Convert.toCheckAttnStashMessage(json);
//   const checkAttnStashResultMessage = Convert.toCheckAttnStashResultMessage(json);
//   const checkDirtyMessage = Convert.toCheckDirtyMessage(json);
//   const checkDirtyResultMessage = Convert.toCheckDirtyResultMessage(json);
//   const clearSessionsMessage = Convert.toClearSessionsMessage(json);
//   const clearWarningsMessage = Convert.toClearWarningsMessage(json);
//   const collapseRepoMessage = Convert.toCollapseRepoMessage(json);
//   const commandErrorMessage = Convert.toCommandErrorMessage(json);
//   const commitWIPMessage = Convert.toCommitWIPMessage(json);
//   const commitWIPResultMessage = Convert.toCommitWIPResultMessage(json);
//   const createBranchMessage = Convert.toCreateBranchMessage(json);
//   const createBranchResultMessage = Convert.toCreateBranchResultMessage(json);
//   const createWorktreeFromBranchMessage = Convert.toCreateWorktreeFromBranchMessage(json);
//   const createWorktreeMessage = Convert.toCreateWorktreeMessage(json);
//   const createWorktreeResultMessage = Convert.toCreateWorktreeResultMessage(json);
//   const daemonWarning = Convert.toDaemonWarning(json);
//   const deleteBranchMessage = Convert.toDeleteBranchMessage(json);
//   const deleteBranchResultMessage = Convert.toDeleteBranchResultMessage(json);
//   const deleteCommentMessage = Convert.toDeleteCommentMessage(json);
//   const deleteCommentResultMessage = Convert.toDeleteCommentResultMessage(json);
//   const deleteWorktreeMessage = Convert.toDeleteWorktreeMessage(json);
//   const deleteWorktreeResultMessage = Convert.toDeleteWorktreeResultMessage(json);
//   const detachSessionMessage = Convert.toDetachSessionMessage(json);
//   const ensureRepoMessage = Convert.toEnsureRepoMessage(json);
//   const ensureRepoResultMessage = Convert.toEnsureRepoResultMessage(json);
//   const fetchPRDetailsMessage = Convert.toFetchPRDetailsMessage(json);
//   const fetchPRDetailsResultMessage = Convert.toFetchPRDetailsResultMessage(json);
//   const fetchRemotesMessage = Convert.toFetchRemotesMessage(json);
//   const fetchRemotesResultMessage = Convert.toFetchRemotesResultMessage(json);
//   const fileDiffResultMessage = Convert.toFileDiffResultMessage(json);
//   const getBranchDiffFilesMessage = Convert.toGetBranchDiffFilesMessage(json);
//   const getCommentsMessage = Convert.toGetCommentsMessage(json);
//   const getCommentsResultMessage = Convert.toGetCommentsResultMessage(json);
//   const getDefaultBranchMessage = Convert.toGetDefaultBranchMessage(json);
//   const getDefaultBranchResultMessage = Convert.toGetDefaultBranchResultMessage(json);
//   const getFileDiffMessage = Convert.toGetFileDiffMessage(json);
//   const getRecentLocationsMessage = Convert.toGetRecentLocationsMessage(json);
//   const getRepoInfoMessage = Convert.toGetRepoInfoMessage(json);
//   const getRepoInfoResultMessage = Convert.toGetRepoInfoResultMessage(json);
//   const getReviewStateMessage = Convert.toGetReviewStateMessage(json);
//   const getReviewStateResultMessage = Convert.toGetReviewStateResultMessage(json);
//   const getSettingsMessage = Convert.toGetSettingsMessage(json);
//   const gitFileChange = Convert.toGitFileChange(json);
//   const gitStatusUpdateMessage = Convert.toGitStatusUpdateMessage(json);
//   const heartbeatMessage = Convert.toHeartbeatMessage(json);
//   const heatState = Convert.toHeatState(json);
//   const initialStateMessage = Convert.toInitialStateMessage(json);
//   const injectTestPRMessage = Convert.toInjectTestPRMessage(json);
//   const injectTestSessionMessage = Convert.toInjectTestSessionMessage(json);
//   const killSessionMessage = Convert.toKillSessionMessage(json);
//   const listBranchesMessage = Convert.toListBranchesMessage(json);
//   const listRemoteBranchesMessage = Convert.toListRemoteBranchesMessage(json);
//   const listRemoteBranchesResultMessage = Convert.toListRemoteBranchesResultMessage(json);
//   const listWorktreesMessage = Convert.toListWorktreesMessage(json);
//   const markFileViewedMessage = Convert.toMarkFileViewedMessage(json);
//   const markFileViewedResultMessage = Convert.toMarkFileViewedResultMessage(json);
//   const mergePRMessage = Convert.toMergePRMessage(json);
//   const muteAuthorMessage = Convert.toMuteAuthorMessage(json);
//   const muteMessage = Convert.toMuteMessage(json);
//   const mutePRMessage = Convert.toMutePRMessage(json);
//   const muteRepoMessage = Convert.toMuteRepoMessage(json);
//   const pR = Convert.toPR(json);
//   const pRActionResultMessage = Convert.toPRActionResultMessage(json);
//   const pRRole = Convert.toPRRole(json);
//   const pRVisitedMessage = Convert.toPRVisitedMessage(json);
//   const pRsUpdatedMessage = Convert.toPRsUpdatedMessage(json);
//   const ptyDesyncMessage = Convert.toPtyDesyncMessage(json);
//   const ptyInputMessage = Convert.toPtyInputMessage(json);
//   const ptyOutputMessage = Convert.toPtyOutputMessage(json);
//   const ptyResizeMessage = Convert.toPtyResizeMessage(json);
//   const queryAuthorsMessage = Convert.toQueryAuthorsMessage(json);
//   const queryMessage = Convert.toQueryMessage(json);
//   const queryPRsMessage = Convert.toQueryPRsMessage(json);
//   const queryReposMessage = Convert.toQueryReposMessage(json);
//   const rateLimitedMessage = Convert.toRateLimitedMessage(json);
//   const recentLocation = Convert.toRecentLocation(json);
//   const recentLocationsResultMessage = Convert.toRecentLocationsResultMessage(json);
//   const refreshPRsMessage = Convert.toRefreshPRsMessage(json);
//   const refreshPRsResultMessage = Convert.toRefreshPRsResultMessage(json);
//   const registerMessage = Convert.toRegisterMessage(json);
//   const repoInfo = Convert.toRepoInfo(json);
//   const repoState = Convert.toRepoState(json);
//   const reposUpdatedMessage = Convert.toReposUpdatedMessage(json);
//   const resolveCommentMessage = Convert.toResolveCommentMessage(json);
//   const resolveCommentResultMessage = Convert.toResolveCommentResultMessage(json);
//   const response = Convert.toResponse(json);
//   const reviewCancelledMessage = Convert.toReviewCancelledMessage(json);
//   const reviewChunkMessage = Convert.toReviewChunkMessage(json);
//   const reviewComment = Convert.toReviewComment(json);
//   const reviewCommentResolvedMessage = Convert.toReviewCommentResolvedMessage(json);
//   const reviewCompleteMessage = Convert.toReviewCompleteMessage(json);
//   const reviewFinding = Convert.toReviewFinding(json);
//   const reviewFindingMessage = Convert.toReviewFindingMessage(json);
//   const reviewStartedMessage = Convert.toReviewStartedMessage(json);
//   const reviewState = Convert.toReviewState(json);
//   const reviewToolUse = Convert.toReviewToolUse(json);
//   const reviewToolUseMessage = Convert.toReviewToolUseMessage(json);
//   const session = Convert.toSession(json);
//   const sessionAgent = Convert.toSessionAgent(json);
//   const sessionExitedMessage = Convert.toSessionExitedMessage(json);
//   const sessionRegisteredMessage = Convert.toSessionRegisteredMessage(json);
//   const sessionState = Convert.toSessionState(json);
//   const sessionStateChangedMessage = Convert.toSessionStateChangedMessage(json);
//   const sessionTodosUpdatedMessage = Convert.toSessionTodosUpdatedMessage(json);
//   const sessionUnregisteredMessage = Convert.toSessionUnregisteredMessage(json);
//   const sessionVisualizedMessage = Convert.toSessionVisualizedMessage(json);
//   const sessionsUpdatedMessage = Convert.toSessionsUpdatedMessage(json);
//   const setSettingMessage = Convert.toSetSettingMessage(json);
//   const settingsUpdatedMessage = Convert.toSettingsUpdatedMessage(json);
//   const spawnResultMessage = Convert.toSpawnResultMessage(json);
//   const spawnSessionMessage = Convert.toSpawnSessionMessage(json);
//   const startReviewMessage = Convert.toStartReviewMessage(json);
//   const stashMessage = Convert.toStashMessage(json);
//   const stashPopMessage = Convert.toStashPopMessage(json);
//   const stashPopResultMessage = Convert.toStashPopResultMessage(json);
//   const stashResultMessage = Convert.toStashResultMessage(json);
//   const stateMessage = Convert.toStateMessage(json);
//   const stopMessage = Convert.toStopMessage(json);
//   const subscribeGitStatusMessage = Convert.toSubscribeGitStatusMessage(json);
//   const switchBranchMessage = Convert.toSwitchBranchMessage(json);
//   const switchBranchResultMessage = Convert.toSwitchBranchResultMessage(json);
//   const todosMessage = Convert.toTodosMessage(json);
//   const unregisterMessage = Convert.toUnregisterMessage(json);
//   const unsubscribeGitStatusMessage = Convert.toUnsubscribeGitStatusMessage(json);
//   const updateCommentMessage = Convert.toUpdateCommentMessage(json);
//   const updateCommentResultMessage = Convert.toUpdateCommentResultMessage(json);
//   const webSocketEvent = Convert.toWebSocketEvent(json);
//   const wontFixCommentMessage = Convert.toWontFixCommentMessage(json);
//   const wontFixCommentResultMessage = Convert.toWontFixCommentResultMessage(json);
//   const worktree = Convert.toWorktree(json);
//   const worktreeCreatedEvent = Convert.toWorktreeCreatedEvent(json);
//   const worktreeDeletedEvent = Convert.toWorktreeDeletedEvent(json);
//   const worktreesUpdatedMessage = Convert.toWorktreesUpdatedMessage(json);
//
// These functions will throw an error if the JSON doesn't
// match the expected interface, even if the JSON is valid.

export interface AddCommentMessage {
    cmd:        AddCommentMessageCmd;
    content:    string;
    filepath:   string;
    line_end:   number;
    line_start: number;
    review_id:  string;
    [property: string]: any;
}

export enum AddCommentMessageCmd {
    AddComment = "add_comment",
}

export interface AddCommentResultMessage {
    comment?: Comment;
    error?:   string;
    event:    AddCommentResultMessageEvent;
    success:  boolean;
    [property: string]: any;
}

export interface Comment {
    author:       string;
    content:      string;
    created_at:   string;
    filepath:     string;
    id:           string;
    line_end:     number;
    line_start:   number;
    resolved:     boolean;
    resolved_at?: string;
    resolved_by?: string;
    review_id:    string;
    wont_fix:     boolean;
    wont_fix_at?: string;
    wont_fix_by?: string;
    [property: string]: any;
}

export enum AddCommentResultMessageEvent {
    AddCommentResult = "add_comment_result",
}

export interface ApprovePRMessage {
    cmd: ApprovePRMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum ApprovePRMessageCmd {
    ApprovePR = "approve_pr",
}

export interface AttachResultMessage {
    cols?:                  number;
    error?:                 string;
    event:                  AttachResultMessageEvent;
    id:                     string;
    last_seq?:              number;
    pid?:                   number;
    rows?:                  number;
    running?:               boolean;
    screen_cols?:           number;
    screen_cursor_visible?: boolean;
    screen_cursor_x?:       number;
    screen_cursor_y?:       number;
    screen_rows?:           number;
    screen_snapshot?:       string;
    screen_snapshot_fresh?: boolean;
    scrollback?:            string;
    scrollback_truncated?:  boolean;
    success:                boolean;
    [property: string]: any;
}

export enum AttachResultMessageEvent {
    AttachResult = "attach_result",
}

export interface AttachSessionMessage {
    cmd: AttachSessionMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum AttachSessionMessageCmd {
    AttachSession = "attach_session",
}

export interface AuthorState {
    author: string;
    muted:  boolean;
    [property: string]: any;
}

export interface AuthorsUpdatedMessage {
    authors?: AuthorElement[];
    event:    AuthorsUpdatedMessageEvent;
    [property: string]: any;
}

export interface AuthorElement {
    author: string;
    muted:  boolean;
    [property: string]: any;
}

export enum AuthorsUpdatedMessageEvent {
    AuthorsUpdated = "authors_updated",
}

export interface Branch {
    commit_hash?: string;
    commit_time?: string;
    is_current?:  boolean;
    name:         string;
    [property: string]: any;
}

export interface BranchChangedMessage {
    event:    BranchChangedMessageEvent;
    session?: SessionElement;
    [property: string]: any;
}

export enum BranchChangedMessageEvent {
    BranchChanged = "branch_changed",
}

export interface SessionElement {
    agent:                        SessionAgent;
    branch?:                      string;
    directory:                    string;
    id:                           string;
    is_worktree?:                 boolean;
    label:                        string;
    last_seen:                    string;
    main_repo?:                   string;
    muted:                        boolean;
    needs_review_after_long_run?: boolean;
    state:                        SessionState;
    state_since:                  string;
    state_updated_at:             string;
    todos?:                       string[];
    [property: string]: any;
}

export enum SessionAgent {
    Claude = "claude",
    Codex = "codex",
    Copilot = "copilot",
}

export enum SessionState {
    Idle = "idle",
    Launching = "launching",
    PendingApproval = "pending_approval",
    Unknown = "unknown",
    WaitingInput = "waiting_input",
    Working = "working",
}

export interface BranchDiffFile {
    additions?:       number;
    deletions?:       number;
    has_uncommitted?: boolean;
    old_path?:        string;
    path:             string;
    status:           string;
    [property: string]: any;
}

export interface BranchDiffFilesResultMessage {
    base_ref:  string;
    directory: string;
    error?:    string;
    event:     BranchDiffFilesResultMessageEvent;
    files:     FileElement[];
    success:   boolean;
    [property: string]: any;
}

export enum BranchDiffFilesResultMessageEvent {
    BranchDiffFilesResult = "branch_diff_files_result",
}

export interface FileElement {
    additions?:       number;
    deletions?:       number;
    has_uncommitted?: boolean;
    old_path?:        string;
    path:             string;
    status:           string;
    [property: string]: any;
}

export interface BranchesResultMessage {
    branches: BranchElement[];
    error?:   string;
    event:    BranchesResultMessageEvent;
    success:  boolean;
    [property: string]: any;
}

export interface BranchElement {
    commit_hash?: string;
    commit_time?: string;
    is_current?:  boolean;
    name:         string;
    [property: string]: any;
}

export enum BranchesResultMessageEvent {
    BranchesResult = "branches_result",
}

export interface CancelReviewMessage {
    cmd:       CancelReviewMessageCmd;
    review_id: string;
    [property: string]: any;
}

export enum CancelReviewMessageCmd {
    CancelReview = "cancel_review",
}

export interface CheckAttnStashMessage {
    branch: string;
    cmd:    CheckAttnStashMessageCmd;
    repo:   string;
    [property: string]: any;
}

export enum CheckAttnStashMessageCmd {
    CheckAttnStash = "check_attn_stash",
}

export interface CheckAttnStashResultMessage {
    error?:     string;
    event:      CheckAttnStashResultMessageEvent;
    found:      boolean;
    stash_ref?: string;
    success:    boolean;
    [property: string]: any;
}

export enum CheckAttnStashResultMessageEvent {
    CheckAttnStashResult = "check_attn_stash_result",
}

export interface CheckDirtyMessage {
    cmd:  CheckDirtyMessageCmd;
    repo: string;
    [property: string]: any;
}

export enum CheckDirtyMessageCmd {
    CheckDirty = "check_dirty",
}

export interface CheckDirtyResultMessage {
    dirty:   boolean;
    error?:  string;
    event:   CheckDirtyResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum CheckDirtyResultMessageEvent {
    CheckDirtyResult = "check_dirty_result",
}

export interface ClearSessionsMessage {
    cmd: ClearSessionsMessageCmd;
    [property: string]: any;
}

export enum ClearSessionsMessageCmd {
    ClearSessions = "clear_sessions",
}

export interface ClearWarningsMessage {
    cmd: ClearWarningsMessageCmd;
    [property: string]: any;
}

export enum ClearWarningsMessageCmd {
    ClearWarnings = "clear_warnings",
}

export interface CollapseRepoMessage {
    cmd:       CollapseRepoMessageCmd;
    collapsed: boolean;
    repo:      string;
    [property: string]: any;
}

export enum CollapseRepoMessageCmd {
    CollapseRepo = "collapse_repo",
}

export interface CommandErrorMessage {
    cmd?:    string;
    error:   string;
    event:   CommandErrorMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum CommandErrorMessageEvent {
    CommandError = "command_error",
}

export interface CommitWIPMessage {
    cmd:  CommitWIPMessageCmd;
    repo: string;
    [property: string]: any;
}

export enum CommitWIPMessageCmd {
    CommitWip = "commit_wip",
}

export interface CommitWIPResultMessage {
    error?:  string;
    event:   CommitWIPResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum CommitWIPResultMessageEvent {
    CommitWipResult = "commit_wip_result",
}

export interface CreateBranchMessage {
    branch:    string;
    cmd:       CreateBranchMessageCmd;
    main_repo: string;
    [property: string]: any;
}

export enum CreateBranchMessageCmd {
    CreateBranch = "create_branch",
}

export interface CreateBranchResultMessage {
    branch:  string;
    error?:  string;
    event:   CreateBranchResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum CreateBranchResultMessageEvent {
    CreateBranchResult = "create_branch_result",
}

export interface CreateWorktreeFromBranchMessage {
    branch:    string;
    cmd:       CreateWorktreeFromBranchMessageCmd;
    main_repo: string;
    path?:     string;
    [property: string]: any;
}

export enum CreateWorktreeFromBranchMessageCmd {
    CreateWorktreeFromBranch = "create_worktree_from_branch",
}

export interface CreateWorktreeMessage {
    branch:         string;
    cmd:            CreateWorktreeMessageCmd;
    main_repo:      string;
    path?:          string;
    starting_from?: string;
    [property: string]: any;
}

export enum CreateWorktreeMessageCmd {
    CreateWorktree = "create_worktree",
}

export interface CreateWorktreeResultMessage {
    error?:  string;
    event:   CreateWorktreeResultMessageEvent;
    path?:   string;
    success: boolean;
    [property: string]: any;
}

export enum CreateWorktreeResultMessageEvent {
    CreateWorktreeResult = "create_worktree_result",
}

export interface DaemonWarning {
    code:    string;
    message: string;
    [property: string]: any;
}

export interface DeleteBranchMessage {
    branch:    string;
    cmd:       DeleteBranchMessageCmd;
    force:     boolean;
    main_repo: string;
    [property: string]: any;
}

export enum DeleteBranchMessageCmd {
    DeleteBranch = "delete_branch",
}

export interface DeleteBranchResultMessage {
    branch:  string;
    error?:  string;
    event:   DeleteBranchResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum DeleteBranchResultMessageEvent {
    DeleteBranchResult = "delete_branch_result",
}

export interface DeleteCommentMessage {
    cmd:        DeleteCommentMessageCmd;
    comment_id: string;
    [property: string]: any;
}

export enum DeleteCommentMessageCmd {
    DeleteComment = "delete_comment",
}

export interface DeleteCommentResultMessage {
    error?:  string;
    event:   DeleteCommentResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum DeleteCommentResultMessageEvent {
    DeleteCommentResult = "delete_comment_result",
}

export interface DeleteWorktreeMessage {
    cmd:  DeleteWorktreeMessageCmd;
    path: string;
    [property: string]: any;
}

export enum DeleteWorktreeMessageCmd {
    DeleteWorktree = "delete_worktree",
}

export interface DeleteWorktreeResultMessage {
    error?:  string;
    event:   DeleteWorktreeResultMessageEvent;
    path:    string;
    success: boolean;
    [property: string]: any;
}

export enum DeleteWorktreeResultMessageEvent {
    DeleteWorktreeResult = "delete_worktree_result",
}

export interface DetachSessionMessage {
    cmd: DetachSessionMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum DetachSessionMessageCmd {
    DetachSession = "detach_session",
}

export interface EnsureRepoMessage {
    clone_url:   string;
    cmd:         EnsureRepoMessageCmd;
    target_path: string;
    [property: string]: any;
}

export enum EnsureRepoMessageCmd {
    EnsureRepo = "ensure_repo",
}

export interface EnsureRepoResultMessage {
    cloned?:      boolean;
    error?:       string;
    event:        EnsureRepoResultMessageEvent;
    success?:     boolean;
    target_path?: string;
    [property: string]: any;
}

export enum EnsureRepoResultMessageEvent {
    EnsureRepoResult = "ensure_repo_result",
}

export interface FetchPRDetailsMessage {
    cmd: FetchPRDetailsMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum FetchPRDetailsMessageCmd {
    FetchPRDetails = "fetch_pr_details",
}

export interface FetchPRDetailsResultMessage {
    error?:  string;
    event:   FetchPRDetailsResultMessageEvent;
    prs?:    PRElement[];
    success: boolean;
    [property: string]: any;
}

export enum FetchPRDetailsResultMessageEvent {
    FetchPRDetailsResult = "fetch_pr_details_result",
}

export interface PRElement {
    approved_by_me:         boolean;
    author:                 string;
    ci_status?:             string;
    comment_count?:         number;
    details_fetched:        boolean;
    details_fetched_at?:    string;
    has_new_changes:        boolean;
    head_branch?:           string;
    head_sha?:              string;
    heat_state?:            HeatState;
    host:                   string;
    id:                     string;
    last_heat_activity_at?: string;
    last_polled:            string;
    last_updated:           string;
    mergeable?:             boolean;
    mergeable_state?:       string;
    muted:                  boolean;
    number:                 number;
    reason:                 string;
    repo:                   string;
    review_status?:         string;
    role:                   PRRole;
    state:                  string;
    title:                  string;
    url:                    string;
    [property: string]: any;
}

export enum HeatState {
    Cold = "cold",
    Hot = "hot",
    Warm = "warm",
}

export enum PRRole {
    Author = "author",
    Reviewer = "reviewer",
}

export interface FetchRemotesMessage {
    cmd:  FetchRemotesMessageCmd;
    repo: string;
    [property: string]: any;
}

export enum FetchRemotesMessageCmd {
    FetchRemotes = "fetch_remotes",
}

export interface FetchRemotesResultMessage {
    error?:  string;
    event:   FetchRemotesResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum FetchRemotesResultMessageEvent {
    FetchRemotesResult = "fetch_remotes_result",
}

export interface FileDiffResultMessage {
    directory: string;
    error?:    string;
    event:     FileDiffResultMessageEvent;
    modified:  string;
    original:  string;
    path:      string;
    success:   boolean;
    [property: string]: any;
}

export enum FileDiffResultMessageEvent {
    FileDiffResult = "file_diff_result",
}

export interface GetBranchDiffFilesMessage {
    base_ref?: string;
    cmd:       GetBranchDiffFilesMessageCmd;
    directory: string;
    [property: string]: any;
}

export enum GetBranchDiffFilesMessageCmd {
    GetBranchDiffFiles = "get_branch_diff_files",
}

export interface GetCommentsMessage {
    cmd:       GetCommentsMessageCmd;
    filepath?: string;
    review_id: string;
    [property: string]: any;
}

export enum GetCommentsMessageCmd {
    GetComments = "get_comments",
}

export interface GetCommentsResultMessage {
    comments?: Comment[];
    error?:    string;
    event:     GetCommentsResultMessageEvent;
    success:   boolean;
    [property: string]: any;
}

export enum GetCommentsResultMessageEvent {
    GetCommentsResult = "get_comments_result",
}

export interface GetDefaultBranchMessage {
    cmd:  GetDefaultBranchMessageCmd;
    repo: string;
    [property: string]: any;
}

export enum GetDefaultBranchMessageCmd {
    GetDefaultBranch = "get_default_branch",
}

export interface GetDefaultBranchResultMessage {
    branch:  string;
    error?:  string;
    event:   GetDefaultBranchResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum GetDefaultBranchResultMessageEvent {
    GetDefaultBranchResult = "get_default_branch_result",
}

export interface GetFileDiffMessage {
    base_ref?: string;
    cmd:       GetFileDiffMessageCmd;
    directory: string;
    path:      string;
    staged?:   boolean;
    [property: string]: any;
}

export enum GetFileDiffMessageCmd {
    GetFileDiff = "get_file_diff",
}

export interface GetRecentLocationsMessage {
    cmd:    GetRecentLocationsMessageCmd;
    limit?: number;
    [property: string]: any;
}

export enum GetRecentLocationsMessageCmd {
    GetRecentLocations = "get_recent_locations",
}

export interface GetRepoInfoMessage {
    cmd:  GetRepoInfoMessageCmd;
    repo: string;
    [property: string]: any;
}

export enum GetRepoInfoMessageCmd {
    GetRepoInfo = "get_repo_info",
}

export interface GetRepoInfoResultMessage {
    error?:  string;
    event:   GetRepoInfoResultMessageEvent;
    info?:   Info;
    success: boolean;
    [property: string]: any;
}

export enum GetRepoInfoResultMessageEvent {
    GetRepoInfoResult = "get_repo_info_result",
}

export interface Info {
    branches:            BranchElement[];
    current_branch:      string;
    current_commit_hash: string;
    current_commit_time: string;
    default_branch:      string;
    fetched_at?:         string;
    repo:                string;
    worktrees:           WorktreeElement[];
    [property: string]: any;
}

export interface WorktreeElement {
    branch:      string;
    created_at?: string;
    main_repo:   string;
    path:        string;
    [property: string]: any;
}

export interface GetReviewStateMessage {
    branch:    string;
    cmd:       GetReviewStateMessageCmd;
    repo_path: string;
    [property: string]: any;
}

export enum GetReviewStateMessageCmd {
    GetReviewState = "get_review_state",
}

export interface GetReviewStateResultMessage {
    error?:  string;
    event:   GetReviewStateResultMessageEvent;
    state?:  State;
    success: boolean;
    [property: string]: any;
}

export enum GetReviewStateResultMessageEvent {
    GetReviewStateResult = "get_review_state_result",
}

export interface State {
    branch:       string;
    repo_path:    string;
    review_id:    string;
    viewed_files: string[];
    [property: string]: any;
}

export interface GetSettingsMessage {
    cmd: GetSettingsMessageCmd;
    [property: string]: any;
}

export enum GetSettingsMessageCmd {
    GetSettings = "get_settings",
}

export interface GitFileChange {
    additions?: number;
    deletions?: number;
    old_path?:  string;
    path:       string;
    status:     string;
    [property: string]: any;
}

export interface GitStatusUpdateMessage {
    directory: string;
    error?:    string;
    event:     GitStatusUpdateMessageEvent;
    staged:    StagedElement[];
    unstaged:  StagedElement[];
    untracked: StagedElement[];
    [property: string]: any;
}

export enum GitStatusUpdateMessageEvent {
    GitStatusUpdate = "git_status_update",
}

export interface StagedElement {
    additions?: number;
    deletions?: number;
    old_path?:  string;
    path:       string;
    status:     string;
    [property: string]: any;
}

export interface HeartbeatMessage {
    cmd: HeartbeatMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum HeartbeatMessageCmd {
    Heartbeat = "heartbeat",
}

export interface InitialStateMessage {
    authors?:            AuthorElement[];
    daemon_instance_id?: string;
    event:               InitialStateMessageEvent;
    protocol_version?:   string;
    prs?:                PRElement[];
    repos?:              RepoElement[];
    sessions?:           SessionElement[];
    settings?:           { [key: string]: any };
    warnings?:           WarningElement[];
    [property: string]: any;
}

export enum InitialStateMessageEvent {
    InitialState = "initial_state",
}

export interface RepoElement {
    collapsed: boolean;
    muted:     boolean;
    repo:      string;
    [property: string]: any;
}

export interface WarningElement {
    code:    string;
    message: string;
    [property: string]: any;
}

export interface InjectTestPRMessage {
    cmd: InjectTestPRMessageCmd;
    pr:  PRElement;
    [property: string]: any;
}

export enum InjectTestPRMessageCmd {
    InjectTestPR = "inject_test_pr",
}

export interface InjectTestSessionMessage {
    cmd:     InjectTestSessionMessageCmd;
    session: SessionElement;
    [property: string]: any;
}

export enum InjectTestSessionMessageCmd {
    InjectTestSession = "inject_test_session",
}

export interface KillSessionMessage {
    cmd:     KillSessionMessageCmd;
    id:      string;
    signal?: string;
    [property: string]: any;
}

export enum KillSessionMessageCmd {
    KillSession = "kill_session",
}

export interface ListBranchesMessage {
    cmd:       ListBranchesMessageCmd;
    main_repo: string;
    [property: string]: any;
}

export enum ListBranchesMessageCmd {
    ListBranches = "list_branches",
}

export interface ListRemoteBranchesMessage {
    cmd:  ListRemoteBranchesMessageCmd;
    repo: string;
    [property: string]: any;
}

export enum ListRemoteBranchesMessageCmd {
    ListRemoteBranches = "list_remote_branches",
}

export interface ListRemoteBranchesResultMessage {
    branches: BranchElement[];
    error?:   string;
    event:    ListRemoteBranchesResultMessageEvent;
    success:  boolean;
    [property: string]: any;
}

export enum ListRemoteBranchesResultMessageEvent {
    ListRemoteBranchesResult = "list_remote_branches_result",
}

export interface ListWorktreesMessage {
    cmd:       ListWorktreesMessageCmd;
    main_repo: string;
    [property: string]: any;
}

export enum ListWorktreesMessageCmd {
    ListWorktrees = "list_worktrees",
}

export interface MarkFileViewedMessage {
    cmd:       MarkFileViewedMessageCmd;
    filepath:  string;
    review_id: string;
    viewed:    boolean;
    [property: string]: any;
}

export enum MarkFileViewedMessageCmd {
    MarkFileViewed = "mark_file_viewed",
}

export interface MarkFileViewedResultMessage {
    error?:    string;
    event:     MarkFileViewedResultMessageEvent;
    filepath:  string;
    review_id: string;
    success:   boolean;
    viewed:    boolean;
    [property: string]: any;
}

export enum MarkFileViewedResultMessageEvent {
    MarkFileViewedResult = "mark_file_viewed_result",
}

export interface MergePRMessage {
    cmd:    MergePRMessageCmd;
    id:     string;
    method: string;
    [property: string]: any;
}

export enum MergePRMessageCmd {
    MergePR = "merge_pr",
}

export interface MuteAuthorMessage {
    author: string;
    cmd:    MuteAuthorMessageCmd;
    [property: string]: any;
}

export enum MuteAuthorMessageCmd {
    MuteAuthor = "mute_author",
}

export interface MuteMessage {
    cmd: MuteMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum MuteMessageCmd {
    Mute = "mute",
}

export interface MutePRMessage {
    cmd: MutePRMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum MutePRMessageCmd {
    MutePR = "mute_pr",
}

export interface MuteRepoMessage {
    cmd:  MuteRepoMessageCmd;
    repo: string;
    [property: string]: any;
}

export enum MuteRepoMessageCmd {
    MuteRepo = "mute_repo",
}

export interface PR {
    approved_by_me:         boolean;
    author:                 string;
    ci_status?:             string;
    comment_count?:         number;
    details_fetched:        boolean;
    details_fetched_at?:    string;
    has_new_changes:        boolean;
    head_branch?:           string;
    head_sha?:              string;
    heat_state?:            HeatState;
    host:                   string;
    id:                     string;
    last_heat_activity_at?: string;
    last_polled:            string;
    last_updated:           string;
    mergeable?:             boolean;
    mergeable_state?:       string;
    muted:                  boolean;
    number:                 number;
    reason:                 string;
    repo:                   string;
    review_status?:         string;
    role:                   PRRole;
    state:                  string;
    title:                  string;
    url:                    string;
    [property: string]: any;
}

export interface PRActionResultMessage {
    action:  string;
    error?:  string;
    event:   PRActionResultMessageEvent;
    id:      string;
    success: boolean;
    [property: string]: any;
}

export enum PRActionResultMessageEvent {
    PRActionResult = "pr_action_result",
}

export interface PRVisitedMessage {
    cmd: PRVisitedMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum PRVisitedMessageCmd {
    PRVisited = "pr_visited",
}

export interface PRsUpdatedMessage {
    event: PRsUpdatedMessageEvent;
    prs?:  PRElement[];
    [property: string]: any;
}

export enum PRsUpdatedMessageEvent {
    PrsUpdated = "prs_updated",
}

export interface PtyDesyncMessage {
    event:  PtyDesyncMessageEvent;
    id:     string;
    reason: string;
    [property: string]: any;
}

export enum PtyDesyncMessageEvent {
    PtyDesync = "pty_desync",
}

export interface PtyInputMessage {
    cmd:  PtyInputMessageCmd;
    data: string;
    id:   string;
    [property: string]: any;
}

export enum PtyInputMessageCmd {
    PtyInput = "pty_input",
}

export interface PtyOutputMessage {
    data:  string;
    event: PtyOutputMessageEvent;
    id:    string;
    seq:   number;
    [property: string]: any;
}

export enum PtyOutputMessageEvent {
    PtyOutput = "pty_output",
}

export interface PtyResizeMessage {
    cmd:  PtyResizeMessageCmd;
    cols: number;
    id:   string;
    rows: number;
    [property: string]: any;
}

export enum PtyResizeMessageCmd {
    PtyResize = "pty_resize",
}

export interface QueryAuthorsMessage {
    cmd: QueryAuthorsMessageCmd;
    [property: string]: any;
}

export enum QueryAuthorsMessageCmd {
    QueryAuthors = "query_authors",
}

export interface QueryMessage {
    cmd:     QueryMessageCmd;
    filter?: string;
    [property: string]: any;
}

export enum QueryMessageCmd {
    Query = "query",
}

export interface QueryPRsMessage {
    cmd:     QueryPRsMessageCmd;
    filter?: string;
    [property: string]: any;
}

export enum QueryPRsMessageCmd {
    QueryPrs = "query_prs",
}

export interface QueryReposMessage {
    cmd:     QueryReposMessageCmd;
    filter?: string;
    [property: string]: any;
}

export enum QueryReposMessageCmd {
    QueryRepos = "query_repos",
}

export interface RateLimitedMessage {
    event:               RateLimitedMessageEvent;
    rate_limit_reset_at: string;
    rate_limit_resource: string;
    [property: string]: any;
}

export enum RateLimitedMessageEvent {
    RateLimited = "rate_limited",
}

export interface RecentLocation {
    label:     string;
    last_seen: string;
    path:      string;
    use_count: number;
    [property: string]: any;
}

export interface RecentLocationsResultMessage {
    error?:           string;
    event:            RecentLocationsResultMessageEvent;
    recent_locations: RecentLocationElement[];
    success:          boolean;
    [property: string]: any;
}

export enum RecentLocationsResultMessageEvent {
    RecentLocationsResult = "recent_locations_result",
}

export interface RecentLocationElement {
    label:     string;
    last_seen: string;
    path:      string;
    use_count: number;
    [property: string]: any;
}

export interface RefreshPRsMessage {
    cmd: RefreshPRsMessageCmd;
    [property: string]: any;
}

export enum RefreshPRsMessageCmd {
    RefreshPrs = "refresh_prs",
}

export interface RefreshPRsResultMessage {
    error?:  string;
    event:   RefreshPRsResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum RefreshPRsResultMessageEvent {
    RefreshPrsResult = "refresh_prs_result",
}

export interface RegisterMessage {
    agent?: SessionAgent;
    cmd:    RegisterMessageCmd;
    dir:    string;
    id:     string;
    label?: string;
    [property: string]: any;
}

export enum RegisterMessageCmd {
    Register = "register",
}

export interface RepoInfo {
    branches:            BranchElement[];
    current_branch:      string;
    current_commit_hash: string;
    current_commit_time: string;
    default_branch:      string;
    fetched_at?:         string;
    repo:                string;
    worktrees:           WorktreeElement[];
    [property: string]: any;
}

export interface RepoState {
    collapsed: boolean;
    muted:     boolean;
    repo:      string;
    [property: string]: any;
}

export interface ReposUpdatedMessage {
    event:  ReposUpdatedMessageEvent;
    repos?: RepoElement[];
    [property: string]: any;
}

export enum ReposUpdatedMessageEvent {
    ReposUpdated = "repos_updated",
}

export interface ResolveCommentMessage {
    cmd:        ResolveCommentMessageCmd;
    comment_id: string;
    resolved:   boolean;
    [property: string]: any;
}

export enum ResolveCommentMessageCmd {
    ResolveComment = "resolve_comment",
}

export interface ResolveCommentResultMessage {
    error?:  string;
    event:   ResolveCommentResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum ResolveCommentResultMessageEvent {
    ResolveCommentResult = "resolve_comment_result",
}

export interface Response {
    authors?:  AuthorElement[];
    error?:    string;
    ok:        boolean;
    prs?:      PRElement[];
    repos?:    RepoElement[];
    sessions?: SessionElement[];
    [property: string]: any;
}

export interface ReviewCancelledMessage {
    event:     ReviewCancelledMessageEvent;
    review_id: string;
    [property: string]: any;
}

export enum ReviewCancelledMessageEvent {
    ReviewCancelled = "review_cancelled",
}

export interface ReviewChunkMessage {
    content:   string;
    event:     ReviewChunkMessageEvent;
    review_id: string;
    [property: string]: any;
}

export enum ReviewChunkMessageEvent {
    ReviewChunk = "review_chunk",
}

export interface ReviewComment {
    author:       string;
    content:      string;
    created_at:   string;
    filepath:     string;
    id:           string;
    line_end:     number;
    line_start:   number;
    resolved:     boolean;
    resolved_at?: string;
    resolved_by?: string;
    review_id:    string;
    wont_fix:     boolean;
    wont_fix_at?: string;
    wont_fix_by?: string;
    [property: string]: any;
}

export interface ReviewCommentResolvedMessage {
    comment_id: string;
    event:      ReviewCommentResolvedMessageEvent;
    review_id:  string;
    [property: string]: any;
}

export enum ReviewCommentResolvedMessageEvent {
    ReviewCommentResolved = "review_comment_resolved",
}

export interface ReviewCompleteMessage {
    error?:    string;
    event:     ReviewCompleteMessageEvent;
    review_id: string;
    success:   boolean;
    [property: string]: any;
}

export enum ReviewCompleteMessageEvent {
    ReviewComplete = "review_complete",
}

export interface ReviewFinding {
    content:    string;
    filepath:   string;
    line_end:   number;
    line_start: number;
    severity?:  string;
    [property: string]: any;
}

export interface ReviewFindingMessage {
    comment?:  Comment;
    event:     ReviewFindingMessageEvent;
    finding:   Finding;
    review_id: string;
    [property: string]: any;
}

export enum ReviewFindingMessageEvent {
    ReviewFinding = "review_finding",
}

export interface Finding {
    content:    string;
    filepath:   string;
    line_end:   number;
    line_start: number;
    severity?:  string;
    [property: string]: any;
}

export interface ReviewStartedMessage {
    event:     ReviewStartedMessageEvent;
    review_id: string;
    [property: string]: any;
}

export enum ReviewStartedMessageEvent {
    ReviewStarted = "review_started",
}

export interface ReviewState {
    branch:       string;
    repo_path:    string;
    review_id:    string;
    viewed_files: string[];
    [property: string]: any;
}

export interface ReviewToolUse {
    input:  { [key: string]: any };
    name:   string;
    output: string;
    [property: string]: any;
}

export interface ReviewToolUseMessage {
    event:     ReviewToolUseMessageEvent;
    review_id: string;
    tool_use:  ToolUse;
    [property: string]: any;
}

export enum ReviewToolUseMessageEvent {
    ReviewToolUse = "review_tool_use",
}

export interface ToolUse {
    input:  { [key: string]: any };
    name:   string;
    output: string;
    [property: string]: any;
}

export interface Session {
    agent:                        SessionAgent;
    branch?:                      string;
    directory:                    string;
    id:                           string;
    is_worktree?:                 boolean;
    label:                        string;
    last_seen:                    string;
    main_repo?:                   string;
    muted:                        boolean;
    needs_review_after_long_run?: boolean;
    state:                        SessionState;
    state_since:                  string;
    state_updated_at:             string;
    todos?:                       string[];
    [property: string]: any;
}

export interface SessionExitedMessage {
    event:     SessionExitedMessageEvent;
    exit_code: number;
    id:        string;
    signal?:   string;
    [property: string]: any;
}

export enum SessionExitedMessageEvent {
    SessionExited = "session_exited",
}

export interface SessionRegisteredMessage {
    event:   SessionRegisteredMessageEvent;
    session: SessionElement;
    [property: string]: any;
}

export enum SessionRegisteredMessageEvent {
    SessionRegistered = "session_registered",
}

export interface SessionStateChangedMessage {
    event:   SessionStateChangedMessageEvent;
    session: SessionElement;
    [property: string]: any;
}

export enum SessionStateChangedMessageEvent {
    SessionStateChanged = "session_state_changed",
}

export interface SessionTodosUpdatedMessage {
    event:   SessionTodosUpdatedMessageEvent;
    session: SessionElement;
    [property: string]: any;
}

export enum SessionTodosUpdatedMessageEvent {
    SessionTodosUpdated = "session_todos_updated",
}

export interface SessionUnregisteredMessage {
    event:   SessionUnregisteredMessageEvent;
    session: SessionElement;
    [property: string]: any;
}

export enum SessionUnregisteredMessageEvent {
    SessionUnregistered = "session_unregistered",
}

export interface SessionVisualizedMessage {
    cmd: SessionVisualizedMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum SessionVisualizedMessageCmd {
    SessionVisualized = "session_visualized",
}

export interface SessionsUpdatedMessage {
    event:     SessionsUpdatedMessageEvent;
    sessions?: SessionElement[];
    [property: string]: any;
}

export enum SessionsUpdatedMessageEvent {
    SessionsUpdated = "sessions_updated",
}

export interface SetSettingMessage {
    cmd:   SetSettingMessageCmd;
    key:   string;
    value: string;
    [property: string]: any;
}

export enum SetSettingMessageCmd {
    SetSetting = "set_setting",
}

export interface SettingsUpdatedMessage {
    error?:    string;
    event:     SettingsUpdatedMessageEvent;
    settings?: { [key: string]: any };
    success?:  boolean;
    [property: string]: any;
}

export enum SettingsUpdatedMessageEvent {
    SettingsUpdated = "settings_updated",
}

export interface SpawnResultMessage {
    error?:  string;
    event:   SpawnResultMessageEvent;
    id:      string;
    success: boolean;
    [property: string]: any;
}

export enum SpawnResultMessageEvent {
    SpawnResult = "spawn_result",
}

export interface SpawnSessionMessage {
    agent:               string;
    claude_executable?:  string;
    cmd:                 SpawnSessionMessageCmd;
    codex_executable?:   string;
    cols:                number;
    copilot_executable?: string;
    cwd:                 string;
    fork_session?:       boolean;
    id:                  string;
    label?:              string;
    resume_picker?:      boolean;
    resume_session_id?:  string;
    rows:                number;
    [property: string]: any;
}

export enum SpawnSessionMessageCmd {
    SpawnSession = "spawn_session",
}

export interface StartReviewMessage {
    base_branch: string;
    branch:      string;
    cmd:         StartReviewMessageCmd;
    repo_path:   string;
    review_id:   string;
    [property: string]: any;
}

export enum StartReviewMessageCmd {
    StartReview = "start_review",
}

export interface StashMessage {
    cmd:     StashMessageCmd;
    message: string;
    repo:    string;
    [property: string]: any;
}

export enum StashMessageCmd {
    Stash = "stash",
}

export interface StashPopMessage {
    cmd:  StashPopMessageCmd;
    repo: string;
    [property: string]: any;
}

export enum StashPopMessageCmd {
    StashPop = "stash_pop",
}

export interface StashPopResultMessage {
    conflict?: boolean;
    error?:    string;
    event:     StashPopResultMessageEvent;
    success:   boolean;
    [property: string]: any;
}

export enum StashPopResultMessageEvent {
    StashPopResult = "stash_pop_result",
}

export interface StashResultMessage {
    error?:  string;
    event:   StashResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum StashResultMessageEvent {
    StashResult = "stash_result",
}

export interface StateMessage {
    cmd:   StateMessageCmd;
    id:    string;
    state: string;
    [property: string]: any;
}

export enum StateMessageCmd {
    State = "state",
}

export interface StopMessage {
    cmd:             StopMessageCmd;
    id:              string;
    transcript_path: string;
    [property: string]: any;
}

export enum StopMessageCmd {
    Stop = "stop",
}

export interface SubscribeGitStatusMessage {
    cmd:       SubscribeGitStatusMessageCmd;
    directory: string;
    [property: string]: any;
}

export enum SubscribeGitStatusMessageCmd {
    SubscribeGitStatus = "subscribe_git_status",
}

export interface SwitchBranchMessage {
    branch:    string;
    cmd:       SwitchBranchMessageCmd;
    main_repo: string;
    [property: string]: any;
}

export enum SwitchBranchMessageCmd {
    SwitchBranch = "switch_branch",
}

export interface SwitchBranchResultMessage {
    branch:  string;
    error?:  string;
    event:   SwitchBranchResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum SwitchBranchResultMessageEvent {
    SwitchBranchResult = "switch_branch_result",
}

export interface TodosMessage {
    cmd:   TodosMessageCmd;
    id:    string;
    todos: string[];
    [property: string]: any;
}

export enum TodosMessageCmd {
    Todos = "todos",
}

export interface UnregisterMessage {
    cmd: UnregisterMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum UnregisterMessageCmd {
    Unregister = "unregister",
}

export interface UnsubscribeGitStatusMessage {
    cmd: UnsubscribeGitStatusMessageCmd;
    [property: string]: any;
}

export enum UnsubscribeGitStatusMessageCmd {
    UnsubscribeGitStatus = "unsubscribe_git_status",
}

export interface UpdateCommentMessage {
    cmd:        UpdateCommentMessageCmd;
    comment_id: string;
    content:    string;
    [property: string]: any;
}

export enum UpdateCommentMessageCmd {
    UpdateComment = "update_comment",
}

export interface UpdateCommentResultMessage {
    error?:  string;
    event:   UpdateCommentResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum UpdateCommentResultMessageEvent {
    UpdateCommentResult = "update_comment_result",
}

export interface WebSocketEvent {
    action?:                string;
    authors?:               AuthorElement[];
    base_ref?:              string;
    branch?:                string;
    branches?:              BranchElement[];
    cloned?:                boolean;
    cmd?:                   string;
    cols?:                  number;
    comment?:               Comment;
    comment_id?:            string;
    conflict?:              boolean;
    content?:               string;
    data?:                  string;
    directory?:             string;
    dirty?:                 boolean;
    error?:                 string;
    event:                  string;
    exit_code?:             number;
    files?:                 FileElement[];
    finding?:               Finding;
    found?:                 boolean;
    id?:                    string;
    last_seq?:              number;
    modified?:              string;
    original?:              string;
    path?:                  string;
    pid?:                   number;
    protocol_version?:      string;
    prs?:                   PRElement[];
    rate_limit_reset_at?:   string;
    rate_limit_resource?:   string;
    reason?:                string;
    recent_locations?:      RecentLocationElement[];
    repos?:                 RepoElement[];
    review_id?:             string;
    rows?:                  number;
    running?:               boolean;
    screen_cols?:           number;
    screen_cursor_visible?: boolean;
    screen_cursor_x?:       number;
    screen_cursor_y?:       number;
    screen_rows?:           number;
    screen_snapshot?:       string;
    screen_snapshot_fresh?: boolean;
    scrollback?:            string;
    scrollback_truncated?:  boolean;
    seq?:                   number;
    session?:               SessionElement;
    sessions?:              SessionElement[];
    settings?:              { [key: string]: any };
    signal?:                string;
    staged?:                StagedElement[];
    stash_ref?:             string;
    success?:               boolean;
    target_path?:           string;
    tool_use?:              ToolUse;
    unstaged?:              StagedElement[];
    untracked?:             StagedElement[];
    warnings?:              WarningElement[];
    worktrees?:             WorktreeElement[];
    [property: string]: any;
}

export interface WontFixCommentMessage {
    cmd:        WontFixCommentMessageCmd;
    comment_id: string;
    wont_fix:   boolean;
    [property: string]: any;
}

export enum WontFixCommentMessageCmd {
    WontFixComment = "wont_fix_comment",
}

export interface WontFixCommentResultMessage {
    error?:  string;
    event:   WontFixCommentResultMessageEvent;
    success: boolean;
    [property: string]: any;
}

export enum WontFixCommentResultMessageEvent {
    WontFixCommentResult = "wont_fix_comment_result",
}

export interface Worktree {
    branch:      string;
    created_at?: string;
    main_repo:   string;
    path:        string;
    [property: string]: any;
}

export interface WorktreeCreatedEvent {
    event:     WorktreeCreatedEventEvent;
    worktrees: WorktreeElement[];
    [property: string]: any;
}

export enum WorktreeCreatedEventEvent {
    WorktreeCreated = "worktree_created",
}

export interface WorktreeDeletedEvent {
    event:     WorktreeDeletedEventEvent;
    worktrees: WorktreeElement[];
    [property: string]: any;
}

export enum WorktreeDeletedEventEvent {
    WorktreeDeleted = "worktree_deleted",
}

export interface WorktreesUpdatedMessage {
    event:      WorktreesUpdatedMessageEvent;
    worktrees?: WorktreeElement[];
    [property: string]: any;
}

export enum WorktreesUpdatedMessageEvent {
    WorktreesUpdated = "worktrees_updated",
}

// Converts JSON strings to/from your types
// and asserts the results of JSON.parse at runtime
export class Convert {
    public static toAddCommentMessage(json: string): AddCommentMessage {
        return cast(JSON.parse(json), r("AddCommentMessage"));
    }

    public static addCommentMessageToJson(value: AddCommentMessage): string {
        return JSON.stringify(uncast(value, r("AddCommentMessage")), null, 2);
    }

    public static toAddCommentResultMessage(json: string): AddCommentResultMessage {
        return cast(JSON.parse(json), r("AddCommentResultMessage"));
    }

    public static addCommentResultMessageToJson(value: AddCommentResultMessage): string {
        return JSON.stringify(uncast(value, r("AddCommentResultMessage")), null, 2);
    }

    public static toApprovePRMessage(json: string): ApprovePRMessage {
        return cast(JSON.parse(json), r("ApprovePRMessage"));
    }

    public static approvePRMessageToJson(value: ApprovePRMessage): string {
        return JSON.stringify(uncast(value, r("ApprovePRMessage")), null, 2);
    }

    public static toAttachResultMessage(json: string): AttachResultMessage {
        return cast(JSON.parse(json), r("AttachResultMessage"));
    }

    public static attachResultMessageToJson(value: AttachResultMessage): string {
        return JSON.stringify(uncast(value, r("AttachResultMessage")), null, 2);
    }

    public static toAttachSessionMessage(json: string): AttachSessionMessage {
        return cast(JSON.parse(json), r("AttachSessionMessage"));
    }

    public static attachSessionMessageToJson(value: AttachSessionMessage): string {
        return JSON.stringify(uncast(value, r("AttachSessionMessage")), null, 2);
    }

    public static toAuthorState(json: string): AuthorState {
        return cast(JSON.parse(json), r("AuthorState"));
    }

    public static authorStateToJson(value: AuthorState): string {
        return JSON.stringify(uncast(value, r("AuthorState")), null, 2);
    }

    public static toAuthorsUpdatedMessage(json: string): AuthorsUpdatedMessage {
        return cast(JSON.parse(json), r("AuthorsUpdatedMessage"));
    }

    public static authorsUpdatedMessageToJson(value: AuthorsUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("AuthorsUpdatedMessage")), null, 2);
    }

    public static toBranch(json: string): Branch {
        return cast(JSON.parse(json), r("Branch"));
    }

    public static branchToJson(value: Branch): string {
        return JSON.stringify(uncast(value, r("Branch")), null, 2);
    }

    public static toBranchChangedMessage(json: string): BranchChangedMessage {
        return cast(JSON.parse(json), r("BranchChangedMessage"));
    }

    public static branchChangedMessageToJson(value: BranchChangedMessage): string {
        return JSON.stringify(uncast(value, r("BranchChangedMessage")), null, 2);
    }

    public static toBranchDiffFile(json: string): BranchDiffFile {
        return cast(JSON.parse(json), r("BranchDiffFile"));
    }

    public static branchDiffFileToJson(value: BranchDiffFile): string {
        return JSON.stringify(uncast(value, r("BranchDiffFile")), null, 2);
    }

    public static toBranchDiffFilesResultMessage(json: string): BranchDiffFilesResultMessage {
        return cast(JSON.parse(json), r("BranchDiffFilesResultMessage"));
    }

    public static branchDiffFilesResultMessageToJson(value: BranchDiffFilesResultMessage): string {
        return JSON.stringify(uncast(value, r("BranchDiffFilesResultMessage")), null, 2);
    }

    public static toBranchesResultMessage(json: string): BranchesResultMessage {
        return cast(JSON.parse(json), r("BranchesResultMessage"));
    }

    public static branchesResultMessageToJson(value: BranchesResultMessage): string {
        return JSON.stringify(uncast(value, r("BranchesResultMessage")), null, 2);
    }

    public static toCancelReviewMessage(json: string): CancelReviewMessage {
        return cast(JSON.parse(json), r("CancelReviewMessage"));
    }

    public static cancelReviewMessageToJson(value: CancelReviewMessage): string {
        return JSON.stringify(uncast(value, r("CancelReviewMessage")), null, 2);
    }

    public static toCheckAttnStashMessage(json: string): CheckAttnStashMessage {
        return cast(JSON.parse(json), r("CheckAttnStashMessage"));
    }

    public static checkAttnStashMessageToJson(value: CheckAttnStashMessage): string {
        return JSON.stringify(uncast(value, r("CheckAttnStashMessage")), null, 2);
    }

    public static toCheckAttnStashResultMessage(json: string): CheckAttnStashResultMessage {
        return cast(JSON.parse(json), r("CheckAttnStashResultMessage"));
    }

    public static checkAttnStashResultMessageToJson(value: CheckAttnStashResultMessage): string {
        return JSON.stringify(uncast(value, r("CheckAttnStashResultMessage")), null, 2);
    }

    public static toCheckDirtyMessage(json: string): CheckDirtyMessage {
        return cast(JSON.parse(json), r("CheckDirtyMessage"));
    }

    public static checkDirtyMessageToJson(value: CheckDirtyMessage): string {
        return JSON.stringify(uncast(value, r("CheckDirtyMessage")), null, 2);
    }

    public static toCheckDirtyResultMessage(json: string): CheckDirtyResultMessage {
        return cast(JSON.parse(json), r("CheckDirtyResultMessage"));
    }

    public static checkDirtyResultMessageToJson(value: CheckDirtyResultMessage): string {
        return JSON.stringify(uncast(value, r("CheckDirtyResultMessage")), null, 2);
    }

    public static toClearSessionsMessage(json: string): ClearSessionsMessage {
        return cast(JSON.parse(json), r("ClearSessionsMessage"));
    }

    public static clearSessionsMessageToJson(value: ClearSessionsMessage): string {
        return JSON.stringify(uncast(value, r("ClearSessionsMessage")), null, 2);
    }

    public static toClearWarningsMessage(json: string): ClearWarningsMessage {
        return cast(JSON.parse(json), r("ClearWarningsMessage"));
    }

    public static clearWarningsMessageToJson(value: ClearWarningsMessage): string {
        return JSON.stringify(uncast(value, r("ClearWarningsMessage")), null, 2);
    }

    public static toCollapseRepoMessage(json: string): CollapseRepoMessage {
        return cast(JSON.parse(json), r("CollapseRepoMessage"));
    }

    public static collapseRepoMessageToJson(value: CollapseRepoMessage): string {
        return JSON.stringify(uncast(value, r("CollapseRepoMessage")), null, 2);
    }

    public static toCommandErrorMessage(json: string): CommandErrorMessage {
        return cast(JSON.parse(json), r("CommandErrorMessage"));
    }

    public static commandErrorMessageToJson(value: CommandErrorMessage): string {
        return JSON.stringify(uncast(value, r("CommandErrorMessage")), null, 2);
    }

    public static toCommitWIPMessage(json: string): CommitWIPMessage {
        return cast(JSON.parse(json), r("CommitWIPMessage"));
    }

    public static commitWIPMessageToJson(value: CommitWIPMessage): string {
        return JSON.stringify(uncast(value, r("CommitWIPMessage")), null, 2);
    }

    public static toCommitWIPResultMessage(json: string): CommitWIPResultMessage {
        return cast(JSON.parse(json), r("CommitWIPResultMessage"));
    }

    public static commitWIPResultMessageToJson(value: CommitWIPResultMessage): string {
        return JSON.stringify(uncast(value, r("CommitWIPResultMessage")), null, 2);
    }

    public static toCreateBranchMessage(json: string): CreateBranchMessage {
        return cast(JSON.parse(json), r("CreateBranchMessage"));
    }

    public static createBranchMessageToJson(value: CreateBranchMessage): string {
        return JSON.stringify(uncast(value, r("CreateBranchMessage")), null, 2);
    }

    public static toCreateBranchResultMessage(json: string): CreateBranchResultMessage {
        return cast(JSON.parse(json), r("CreateBranchResultMessage"));
    }

    public static createBranchResultMessageToJson(value: CreateBranchResultMessage): string {
        return JSON.stringify(uncast(value, r("CreateBranchResultMessage")), null, 2);
    }

    public static toCreateWorktreeFromBranchMessage(json: string): CreateWorktreeFromBranchMessage {
        return cast(JSON.parse(json), r("CreateWorktreeFromBranchMessage"));
    }

    public static createWorktreeFromBranchMessageToJson(value: CreateWorktreeFromBranchMessage): string {
        return JSON.stringify(uncast(value, r("CreateWorktreeFromBranchMessage")), null, 2);
    }

    public static toCreateWorktreeMessage(json: string): CreateWorktreeMessage {
        return cast(JSON.parse(json), r("CreateWorktreeMessage"));
    }

    public static createWorktreeMessageToJson(value: CreateWorktreeMessage): string {
        return JSON.stringify(uncast(value, r("CreateWorktreeMessage")), null, 2);
    }

    public static toCreateWorktreeResultMessage(json: string): CreateWorktreeResultMessage {
        return cast(JSON.parse(json), r("CreateWorktreeResultMessage"));
    }

    public static createWorktreeResultMessageToJson(value: CreateWorktreeResultMessage): string {
        return JSON.stringify(uncast(value, r("CreateWorktreeResultMessage")), null, 2);
    }

    public static toDaemonWarning(json: string): DaemonWarning {
        return cast(JSON.parse(json), r("DaemonWarning"));
    }

    public static daemonWarningToJson(value: DaemonWarning): string {
        return JSON.stringify(uncast(value, r("DaemonWarning")), null, 2);
    }

    public static toDeleteBranchMessage(json: string): DeleteBranchMessage {
        return cast(JSON.parse(json), r("DeleteBranchMessage"));
    }

    public static deleteBranchMessageToJson(value: DeleteBranchMessage): string {
        return JSON.stringify(uncast(value, r("DeleteBranchMessage")), null, 2);
    }

    public static toDeleteBranchResultMessage(json: string): DeleteBranchResultMessage {
        return cast(JSON.parse(json), r("DeleteBranchResultMessage"));
    }

    public static deleteBranchResultMessageToJson(value: DeleteBranchResultMessage): string {
        return JSON.stringify(uncast(value, r("DeleteBranchResultMessage")), null, 2);
    }

    public static toDeleteCommentMessage(json: string): DeleteCommentMessage {
        return cast(JSON.parse(json), r("DeleteCommentMessage"));
    }

    public static deleteCommentMessageToJson(value: DeleteCommentMessage): string {
        return JSON.stringify(uncast(value, r("DeleteCommentMessage")), null, 2);
    }

    public static toDeleteCommentResultMessage(json: string): DeleteCommentResultMessage {
        return cast(JSON.parse(json), r("DeleteCommentResultMessage"));
    }

    public static deleteCommentResultMessageToJson(value: DeleteCommentResultMessage): string {
        return JSON.stringify(uncast(value, r("DeleteCommentResultMessage")), null, 2);
    }

    public static toDeleteWorktreeMessage(json: string): DeleteWorktreeMessage {
        return cast(JSON.parse(json), r("DeleteWorktreeMessage"));
    }

    public static deleteWorktreeMessageToJson(value: DeleteWorktreeMessage): string {
        return JSON.stringify(uncast(value, r("DeleteWorktreeMessage")), null, 2);
    }

    public static toDeleteWorktreeResultMessage(json: string): DeleteWorktreeResultMessage {
        return cast(JSON.parse(json), r("DeleteWorktreeResultMessage"));
    }

    public static deleteWorktreeResultMessageToJson(value: DeleteWorktreeResultMessage): string {
        return JSON.stringify(uncast(value, r("DeleteWorktreeResultMessage")), null, 2);
    }

    public static toDetachSessionMessage(json: string): DetachSessionMessage {
        return cast(JSON.parse(json), r("DetachSessionMessage"));
    }

    public static detachSessionMessageToJson(value: DetachSessionMessage): string {
        return JSON.stringify(uncast(value, r("DetachSessionMessage")), null, 2);
    }

    public static toEnsureRepoMessage(json: string): EnsureRepoMessage {
        return cast(JSON.parse(json), r("EnsureRepoMessage"));
    }

    public static ensureRepoMessageToJson(value: EnsureRepoMessage): string {
        return JSON.stringify(uncast(value, r("EnsureRepoMessage")), null, 2);
    }

    public static toEnsureRepoResultMessage(json: string): EnsureRepoResultMessage {
        return cast(JSON.parse(json), r("EnsureRepoResultMessage"));
    }

    public static ensureRepoResultMessageToJson(value: EnsureRepoResultMessage): string {
        return JSON.stringify(uncast(value, r("EnsureRepoResultMessage")), null, 2);
    }

    public static toFetchPRDetailsMessage(json: string): FetchPRDetailsMessage {
        return cast(JSON.parse(json), r("FetchPRDetailsMessage"));
    }

    public static fetchPRDetailsMessageToJson(value: FetchPRDetailsMessage): string {
        return JSON.stringify(uncast(value, r("FetchPRDetailsMessage")), null, 2);
    }

    public static toFetchPRDetailsResultMessage(json: string): FetchPRDetailsResultMessage {
        return cast(JSON.parse(json), r("FetchPRDetailsResultMessage"));
    }

    public static fetchPRDetailsResultMessageToJson(value: FetchPRDetailsResultMessage): string {
        return JSON.stringify(uncast(value, r("FetchPRDetailsResultMessage")), null, 2);
    }

    public static toFetchRemotesMessage(json: string): FetchRemotesMessage {
        return cast(JSON.parse(json), r("FetchRemotesMessage"));
    }

    public static fetchRemotesMessageToJson(value: FetchRemotesMessage): string {
        return JSON.stringify(uncast(value, r("FetchRemotesMessage")), null, 2);
    }

    public static toFetchRemotesResultMessage(json: string): FetchRemotesResultMessage {
        return cast(JSON.parse(json), r("FetchRemotesResultMessage"));
    }

    public static fetchRemotesResultMessageToJson(value: FetchRemotesResultMessage): string {
        return JSON.stringify(uncast(value, r("FetchRemotesResultMessage")), null, 2);
    }

    public static toFileDiffResultMessage(json: string): FileDiffResultMessage {
        return cast(JSON.parse(json), r("FileDiffResultMessage"));
    }

    public static fileDiffResultMessageToJson(value: FileDiffResultMessage): string {
        return JSON.stringify(uncast(value, r("FileDiffResultMessage")), null, 2);
    }

    public static toGetBranchDiffFilesMessage(json: string): GetBranchDiffFilesMessage {
        return cast(JSON.parse(json), r("GetBranchDiffFilesMessage"));
    }

    public static getBranchDiffFilesMessageToJson(value: GetBranchDiffFilesMessage): string {
        return JSON.stringify(uncast(value, r("GetBranchDiffFilesMessage")), null, 2);
    }

    public static toGetCommentsMessage(json: string): GetCommentsMessage {
        return cast(JSON.parse(json), r("GetCommentsMessage"));
    }

    public static getCommentsMessageToJson(value: GetCommentsMessage): string {
        return JSON.stringify(uncast(value, r("GetCommentsMessage")), null, 2);
    }

    public static toGetCommentsResultMessage(json: string): GetCommentsResultMessage {
        return cast(JSON.parse(json), r("GetCommentsResultMessage"));
    }

    public static getCommentsResultMessageToJson(value: GetCommentsResultMessage): string {
        return JSON.stringify(uncast(value, r("GetCommentsResultMessage")), null, 2);
    }

    public static toGetDefaultBranchMessage(json: string): GetDefaultBranchMessage {
        return cast(JSON.parse(json), r("GetDefaultBranchMessage"));
    }

    public static getDefaultBranchMessageToJson(value: GetDefaultBranchMessage): string {
        return JSON.stringify(uncast(value, r("GetDefaultBranchMessage")), null, 2);
    }

    public static toGetDefaultBranchResultMessage(json: string): GetDefaultBranchResultMessage {
        return cast(JSON.parse(json), r("GetDefaultBranchResultMessage"));
    }

    public static getDefaultBranchResultMessageToJson(value: GetDefaultBranchResultMessage): string {
        return JSON.stringify(uncast(value, r("GetDefaultBranchResultMessage")), null, 2);
    }

    public static toGetFileDiffMessage(json: string): GetFileDiffMessage {
        return cast(JSON.parse(json), r("GetFileDiffMessage"));
    }

    public static getFileDiffMessageToJson(value: GetFileDiffMessage): string {
        return JSON.stringify(uncast(value, r("GetFileDiffMessage")), null, 2);
    }

    public static toGetRecentLocationsMessage(json: string): GetRecentLocationsMessage {
        return cast(JSON.parse(json), r("GetRecentLocationsMessage"));
    }

    public static getRecentLocationsMessageToJson(value: GetRecentLocationsMessage): string {
        return JSON.stringify(uncast(value, r("GetRecentLocationsMessage")), null, 2);
    }

    public static toGetRepoInfoMessage(json: string): GetRepoInfoMessage {
        return cast(JSON.parse(json), r("GetRepoInfoMessage"));
    }

    public static getRepoInfoMessageToJson(value: GetRepoInfoMessage): string {
        return JSON.stringify(uncast(value, r("GetRepoInfoMessage")), null, 2);
    }

    public static toGetRepoInfoResultMessage(json: string): GetRepoInfoResultMessage {
        return cast(JSON.parse(json), r("GetRepoInfoResultMessage"));
    }

    public static getRepoInfoResultMessageToJson(value: GetRepoInfoResultMessage): string {
        return JSON.stringify(uncast(value, r("GetRepoInfoResultMessage")), null, 2);
    }

    public static toGetReviewStateMessage(json: string): GetReviewStateMessage {
        return cast(JSON.parse(json), r("GetReviewStateMessage"));
    }

    public static getReviewStateMessageToJson(value: GetReviewStateMessage): string {
        return JSON.stringify(uncast(value, r("GetReviewStateMessage")), null, 2);
    }

    public static toGetReviewStateResultMessage(json: string): GetReviewStateResultMessage {
        return cast(JSON.parse(json), r("GetReviewStateResultMessage"));
    }

    public static getReviewStateResultMessageToJson(value: GetReviewStateResultMessage): string {
        return JSON.stringify(uncast(value, r("GetReviewStateResultMessage")), null, 2);
    }

    public static toGetSettingsMessage(json: string): GetSettingsMessage {
        return cast(JSON.parse(json), r("GetSettingsMessage"));
    }

    public static getSettingsMessageToJson(value: GetSettingsMessage): string {
        return JSON.stringify(uncast(value, r("GetSettingsMessage")), null, 2);
    }

    public static toGitFileChange(json: string): GitFileChange {
        return cast(JSON.parse(json), r("GitFileChange"));
    }

    public static gitFileChangeToJson(value: GitFileChange): string {
        return JSON.stringify(uncast(value, r("GitFileChange")), null, 2);
    }

    public static toGitStatusUpdateMessage(json: string): GitStatusUpdateMessage {
        return cast(JSON.parse(json), r("GitStatusUpdateMessage"));
    }

    public static gitStatusUpdateMessageToJson(value: GitStatusUpdateMessage): string {
        return JSON.stringify(uncast(value, r("GitStatusUpdateMessage")), null, 2);
    }

    public static toHeartbeatMessage(json: string): HeartbeatMessage {
        return cast(JSON.parse(json), r("HeartbeatMessage"));
    }

    public static heartbeatMessageToJson(value: HeartbeatMessage): string {
        return JSON.stringify(uncast(value, r("HeartbeatMessage")), null, 2);
    }

    public static toHeatState(json: string): HeatState {
        return cast(JSON.parse(json), r("HeatState"));
    }

    public static heatStateToJson(value: HeatState): string {
        return JSON.stringify(uncast(value, r("HeatState")), null, 2);
    }

    public static toInitialStateMessage(json: string): InitialStateMessage {
        return cast(JSON.parse(json), r("InitialStateMessage"));
    }

    public static initialStateMessageToJson(value: InitialStateMessage): string {
        return JSON.stringify(uncast(value, r("InitialStateMessage")), null, 2);
    }

    public static toInjectTestPRMessage(json: string): InjectTestPRMessage {
        return cast(JSON.parse(json), r("InjectTestPRMessage"));
    }

    public static injectTestPRMessageToJson(value: InjectTestPRMessage): string {
        return JSON.stringify(uncast(value, r("InjectTestPRMessage")), null, 2);
    }

    public static toInjectTestSessionMessage(json: string): InjectTestSessionMessage {
        return cast(JSON.parse(json), r("InjectTestSessionMessage"));
    }

    public static injectTestSessionMessageToJson(value: InjectTestSessionMessage): string {
        return JSON.stringify(uncast(value, r("InjectTestSessionMessage")), null, 2);
    }

    public static toKillSessionMessage(json: string): KillSessionMessage {
        return cast(JSON.parse(json), r("KillSessionMessage"));
    }

    public static killSessionMessageToJson(value: KillSessionMessage): string {
        return JSON.stringify(uncast(value, r("KillSessionMessage")), null, 2);
    }

    public static toListBranchesMessage(json: string): ListBranchesMessage {
        return cast(JSON.parse(json), r("ListBranchesMessage"));
    }

    public static listBranchesMessageToJson(value: ListBranchesMessage): string {
        return JSON.stringify(uncast(value, r("ListBranchesMessage")), null, 2);
    }

    public static toListRemoteBranchesMessage(json: string): ListRemoteBranchesMessage {
        return cast(JSON.parse(json), r("ListRemoteBranchesMessage"));
    }

    public static listRemoteBranchesMessageToJson(value: ListRemoteBranchesMessage): string {
        return JSON.stringify(uncast(value, r("ListRemoteBranchesMessage")), null, 2);
    }

    public static toListRemoteBranchesResultMessage(json: string): ListRemoteBranchesResultMessage {
        return cast(JSON.parse(json), r("ListRemoteBranchesResultMessage"));
    }

    public static listRemoteBranchesResultMessageToJson(value: ListRemoteBranchesResultMessage): string {
        return JSON.stringify(uncast(value, r("ListRemoteBranchesResultMessage")), null, 2);
    }

    public static toListWorktreesMessage(json: string): ListWorktreesMessage {
        return cast(JSON.parse(json), r("ListWorktreesMessage"));
    }

    public static listWorktreesMessageToJson(value: ListWorktreesMessage): string {
        return JSON.stringify(uncast(value, r("ListWorktreesMessage")), null, 2);
    }

    public static toMarkFileViewedMessage(json: string): MarkFileViewedMessage {
        return cast(JSON.parse(json), r("MarkFileViewedMessage"));
    }

    public static markFileViewedMessageToJson(value: MarkFileViewedMessage): string {
        return JSON.stringify(uncast(value, r("MarkFileViewedMessage")), null, 2);
    }

    public static toMarkFileViewedResultMessage(json: string): MarkFileViewedResultMessage {
        return cast(JSON.parse(json), r("MarkFileViewedResultMessage"));
    }

    public static markFileViewedResultMessageToJson(value: MarkFileViewedResultMessage): string {
        return JSON.stringify(uncast(value, r("MarkFileViewedResultMessage")), null, 2);
    }

    public static toMergePRMessage(json: string): MergePRMessage {
        return cast(JSON.parse(json), r("MergePRMessage"));
    }

    public static mergePRMessageToJson(value: MergePRMessage): string {
        return JSON.stringify(uncast(value, r("MergePRMessage")), null, 2);
    }

    public static toMuteAuthorMessage(json: string): MuteAuthorMessage {
        return cast(JSON.parse(json), r("MuteAuthorMessage"));
    }

    public static muteAuthorMessageToJson(value: MuteAuthorMessage): string {
        return JSON.stringify(uncast(value, r("MuteAuthorMessage")), null, 2);
    }

    public static toMuteMessage(json: string): MuteMessage {
        return cast(JSON.parse(json), r("MuteMessage"));
    }

    public static muteMessageToJson(value: MuteMessage): string {
        return JSON.stringify(uncast(value, r("MuteMessage")), null, 2);
    }

    public static toMutePRMessage(json: string): MutePRMessage {
        return cast(JSON.parse(json), r("MutePRMessage"));
    }

    public static mutePRMessageToJson(value: MutePRMessage): string {
        return JSON.stringify(uncast(value, r("MutePRMessage")), null, 2);
    }

    public static toMuteRepoMessage(json: string): MuteRepoMessage {
        return cast(JSON.parse(json), r("MuteRepoMessage"));
    }

    public static muteRepoMessageToJson(value: MuteRepoMessage): string {
        return JSON.stringify(uncast(value, r("MuteRepoMessage")), null, 2);
    }

    public static toPR(json: string): PR {
        return cast(JSON.parse(json), r("PR"));
    }

    public static pRToJson(value: PR): string {
        return JSON.stringify(uncast(value, r("PR")), null, 2);
    }

    public static toPRActionResultMessage(json: string): PRActionResultMessage {
        return cast(JSON.parse(json), r("PRActionResultMessage"));
    }

    public static pRActionResultMessageToJson(value: PRActionResultMessage): string {
        return JSON.stringify(uncast(value, r("PRActionResultMessage")), null, 2);
    }

    public static toPRRole(json: string): PRRole {
        return cast(JSON.parse(json), r("PRRole"));
    }

    public static pRRoleToJson(value: PRRole): string {
        return JSON.stringify(uncast(value, r("PRRole")), null, 2);
    }

    public static toPRVisitedMessage(json: string): PRVisitedMessage {
        return cast(JSON.parse(json), r("PRVisitedMessage"));
    }

    public static pRVisitedMessageToJson(value: PRVisitedMessage): string {
        return JSON.stringify(uncast(value, r("PRVisitedMessage")), null, 2);
    }

    public static toPRsUpdatedMessage(json: string): PRsUpdatedMessage {
        return cast(JSON.parse(json), r("PRsUpdatedMessage"));
    }

    public static pRsUpdatedMessageToJson(value: PRsUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("PRsUpdatedMessage")), null, 2);
    }

    public static toPtyDesyncMessage(json: string): PtyDesyncMessage {
        return cast(JSON.parse(json), r("PtyDesyncMessage"));
    }

    public static ptyDesyncMessageToJson(value: PtyDesyncMessage): string {
        return JSON.stringify(uncast(value, r("PtyDesyncMessage")), null, 2);
    }

    public static toPtyInputMessage(json: string): PtyInputMessage {
        return cast(JSON.parse(json), r("PtyInputMessage"));
    }

    public static ptyInputMessageToJson(value: PtyInputMessage): string {
        return JSON.stringify(uncast(value, r("PtyInputMessage")), null, 2);
    }

    public static toPtyOutputMessage(json: string): PtyOutputMessage {
        return cast(JSON.parse(json), r("PtyOutputMessage"));
    }

    public static ptyOutputMessageToJson(value: PtyOutputMessage): string {
        return JSON.stringify(uncast(value, r("PtyOutputMessage")), null, 2);
    }

    public static toPtyResizeMessage(json: string): PtyResizeMessage {
        return cast(JSON.parse(json), r("PtyResizeMessage"));
    }

    public static ptyResizeMessageToJson(value: PtyResizeMessage): string {
        return JSON.stringify(uncast(value, r("PtyResizeMessage")), null, 2);
    }

    public static toQueryAuthorsMessage(json: string): QueryAuthorsMessage {
        return cast(JSON.parse(json), r("QueryAuthorsMessage"));
    }

    public static queryAuthorsMessageToJson(value: QueryAuthorsMessage): string {
        return JSON.stringify(uncast(value, r("QueryAuthorsMessage")), null, 2);
    }

    public static toQueryMessage(json: string): QueryMessage {
        return cast(JSON.parse(json), r("QueryMessage"));
    }

    public static queryMessageToJson(value: QueryMessage): string {
        return JSON.stringify(uncast(value, r("QueryMessage")), null, 2);
    }

    public static toQueryPRsMessage(json: string): QueryPRsMessage {
        return cast(JSON.parse(json), r("QueryPRsMessage"));
    }

    public static queryPRsMessageToJson(value: QueryPRsMessage): string {
        return JSON.stringify(uncast(value, r("QueryPRsMessage")), null, 2);
    }

    public static toQueryReposMessage(json: string): QueryReposMessage {
        return cast(JSON.parse(json), r("QueryReposMessage"));
    }

    public static queryReposMessageToJson(value: QueryReposMessage): string {
        return JSON.stringify(uncast(value, r("QueryReposMessage")), null, 2);
    }

    public static toRateLimitedMessage(json: string): RateLimitedMessage {
        return cast(JSON.parse(json), r("RateLimitedMessage"));
    }

    public static rateLimitedMessageToJson(value: RateLimitedMessage): string {
        return JSON.stringify(uncast(value, r("RateLimitedMessage")), null, 2);
    }

    public static toRecentLocation(json: string): RecentLocation {
        return cast(JSON.parse(json), r("RecentLocation"));
    }

    public static recentLocationToJson(value: RecentLocation): string {
        return JSON.stringify(uncast(value, r("RecentLocation")), null, 2);
    }

    public static toRecentLocationsResultMessage(json: string): RecentLocationsResultMessage {
        return cast(JSON.parse(json), r("RecentLocationsResultMessage"));
    }

    public static recentLocationsResultMessageToJson(value: RecentLocationsResultMessage): string {
        return JSON.stringify(uncast(value, r("RecentLocationsResultMessage")), null, 2);
    }

    public static toRefreshPRsMessage(json: string): RefreshPRsMessage {
        return cast(JSON.parse(json), r("RefreshPRsMessage"));
    }

    public static refreshPRsMessageToJson(value: RefreshPRsMessage): string {
        return JSON.stringify(uncast(value, r("RefreshPRsMessage")), null, 2);
    }

    public static toRefreshPRsResultMessage(json: string): RefreshPRsResultMessage {
        return cast(JSON.parse(json), r("RefreshPRsResultMessage"));
    }

    public static refreshPRsResultMessageToJson(value: RefreshPRsResultMessage): string {
        return JSON.stringify(uncast(value, r("RefreshPRsResultMessage")), null, 2);
    }

    public static toRegisterMessage(json: string): RegisterMessage {
        return cast(JSON.parse(json), r("RegisterMessage"));
    }

    public static registerMessageToJson(value: RegisterMessage): string {
        return JSON.stringify(uncast(value, r("RegisterMessage")), null, 2);
    }

    public static toRepoInfo(json: string): RepoInfo {
        return cast(JSON.parse(json), r("RepoInfo"));
    }

    public static repoInfoToJson(value: RepoInfo): string {
        return JSON.stringify(uncast(value, r("RepoInfo")), null, 2);
    }

    public static toRepoState(json: string): RepoState {
        return cast(JSON.parse(json), r("RepoState"));
    }

    public static repoStateToJson(value: RepoState): string {
        return JSON.stringify(uncast(value, r("RepoState")), null, 2);
    }

    public static toReposUpdatedMessage(json: string): ReposUpdatedMessage {
        return cast(JSON.parse(json), r("ReposUpdatedMessage"));
    }

    public static reposUpdatedMessageToJson(value: ReposUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("ReposUpdatedMessage")), null, 2);
    }

    public static toResolveCommentMessage(json: string): ResolveCommentMessage {
        return cast(JSON.parse(json), r("ResolveCommentMessage"));
    }

    public static resolveCommentMessageToJson(value: ResolveCommentMessage): string {
        return JSON.stringify(uncast(value, r("ResolveCommentMessage")), null, 2);
    }

    public static toResolveCommentResultMessage(json: string): ResolveCommentResultMessage {
        return cast(JSON.parse(json), r("ResolveCommentResultMessage"));
    }

    public static resolveCommentResultMessageToJson(value: ResolveCommentResultMessage): string {
        return JSON.stringify(uncast(value, r("ResolveCommentResultMessage")), null, 2);
    }

    public static toResponse(json: string): Response {
        return cast(JSON.parse(json), r("Response"));
    }

    public static responseToJson(value: Response): string {
        return JSON.stringify(uncast(value, r("Response")), null, 2);
    }

    public static toReviewCancelledMessage(json: string): ReviewCancelledMessage {
        return cast(JSON.parse(json), r("ReviewCancelledMessage"));
    }

    public static reviewCancelledMessageToJson(value: ReviewCancelledMessage): string {
        return JSON.stringify(uncast(value, r("ReviewCancelledMessage")), null, 2);
    }

    public static toReviewChunkMessage(json: string): ReviewChunkMessage {
        return cast(JSON.parse(json), r("ReviewChunkMessage"));
    }

    public static reviewChunkMessageToJson(value: ReviewChunkMessage): string {
        return JSON.stringify(uncast(value, r("ReviewChunkMessage")), null, 2);
    }

    public static toReviewComment(json: string): ReviewComment {
        return cast(JSON.parse(json), r("ReviewComment"));
    }

    public static reviewCommentToJson(value: ReviewComment): string {
        return JSON.stringify(uncast(value, r("ReviewComment")), null, 2);
    }

    public static toReviewCommentResolvedMessage(json: string): ReviewCommentResolvedMessage {
        return cast(JSON.parse(json), r("ReviewCommentResolvedMessage"));
    }

    public static reviewCommentResolvedMessageToJson(value: ReviewCommentResolvedMessage): string {
        return JSON.stringify(uncast(value, r("ReviewCommentResolvedMessage")), null, 2);
    }

    public static toReviewCompleteMessage(json: string): ReviewCompleteMessage {
        return cast(JSON.parse(json), r("ReviewCompleteMessage"));
    }

    public static reviewCompleteMessageToJson(value: ReviewCompleteMessage): string {
        return JSON.stringify(uncast(value, r("ReviewCompleteMessage")), null, 2);
    }

    public static toReviewFinding(json: string): ReviewFinding {
        return cast(JSON.parse(json), r("ReviewFinding"));
    }

    public static reviewFindingToJson(value: ReviewFinding): string {
        return JSON.stringify(uncast(value, r("ReviewFinding")), null, 2);
    }

    public static toReviewFindingMessage(json: string): ReviewFindingMessage {
        return cast(JSON.parse(json), r("ReviewFindingMessage"));
    }

    public static reviewFindingMessageToJson(value: ReviewFindingMessage): string {
        return JSON.stringify(uncast(value, r("ReviewFindingMessage")), null, 2);
    }

    public static toReviewStartedMessage(json: string): ReviewStartedMessage {
        return cast(JSON.parse(json), r("ReviewStartedMessage"));
    }

    public static reviewStartedMessageToJson(value: ReviewStartedMessage): string {
        return JSON.stringify(uncast(value, r("ReviewStartedMessage")), null, 2);
    }

    public static toReviewState(json: string): ReviewState {
        return cast(JSON.parse(json), r("ReviewState"));
    }

    public static reviewStateToJson(value: ReviewState): string {
        return JSON.stringify(uncast(value, r("ReviewState")), null, 2);
    }

    public static toReviewToolUse(json: string): ReviewToolUse {
        return cast(JSON.parse(json), r("ReviewToolUse"));
    }

    public static reviewToolUseToJson(value: ReviewToolUse): string {
        return JSON.stringify(uncast(value, r("ReviewToolUse")), null, 2);
    }

    public static toReviewToolUseMessage(json: string): ReviewToolUseMessage {
        return cast(JSON.parse(json), r("ReviewToolUseMessage"));
    }

    public static reviewToolUseMessageToJson(value: ReviewToolUseMessage): string {
        return JSON.stringify(uncast(value, r("ReviewToolUseMessage")), null, 2);
    }

    public static toSession(json: string): Session {
        return cast(JSON.parse(json), r("Session"));
    }

    public static sessionToJson(value: Session): string {
        return JSON.stringify(uncast(value, r("Session")), null, 2);
    }

    public static toSessionAgent(json: string): SessionAgent {
        return cast(JSON.parse(json), r("SessionAgent"));
    }

    public static sessionAgentToJson(value: SessionAgent): string {
        return JSON.stringify(uncast(value, r("SessionAgent")), null, 2);
    }

    public static toSessionExitedMessage(json: string): SessionExitedMessage {
        return cast(JSON.parse(json), r("SessionExitedMessage"));
    }

    public static sessionExitedMessageToJson(value: SessionExitedMessage): string {
        return JSON.stringify(uncast(value, r("SessionExitedMessage")), null, 2);
    }

    public static toSessionRegisteredMessage(json: string): SessionRegisteredMessage {
        return cast(JSON.parse(json), r("SessionRegisteredMessage"));
    }

    public static sessionRegisteredMessageToJson(value: SessionRegisteredMessage): string {
        return JSON.stringify(uncast(value, r("SessionRegisteredMessage")), null, 2);
    }

    public static toSessionState(json: string): SessionState {
        return cast(JSON.parse(json), r("SessionState"));
    }

    public static sessionStateToJson(value: SessionState): string {
        return JSON.stringify(uncast(value, r("SessionState")), null, 2);
    }

    public static toSessionStateChangedMessage(json: string): SessionStateChangedMessage {
        return cast(JSON.parse(json), r("SessionStateChangedMessage"));
    }

    public static sessionStateChangedMessageToJson(value: SessionStateChangedMessage): string {
        return JSON.stringify(uncast(value, r("SessionStateChangedMessage")), null, 2);
    }

    public static toSessionTodosUpdatedMessage(json: string): SessionTodosUpdatedMessage {
        return cast(JSON.parse(json), r("SessionTodosUpdatedMessage"));
    }

    public static sessionTodosUpdatedMessageToJson(value: SessionTodosUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("SessionTodosUpdatedMessage")), null, 2);
    }

    public static toSessionUnregisteredMessage(json: string): SessionUnregisteredMessage {
        return cast(JSON.parse(json), r("SessionUnregisteredMessage"));
    }

    public static sessionUnregisteredMessageToJson(value: SessionUnregisteredMessage): string {
        return JSON.stringify(uncast(value, r("SessionUnregisteredMessage")), null, 2);
    }

    public static toSessionVisualizedMessage(json: string): SessionVisualizedMessage {
        return cast(JSON.parse(json), r("SessionVisualizedMessage"));
    }

    public static sessionVisualizedMessageToJson(value: SessionVisualizedMessage): string {
        return JSON.stringify(uncast(value, r("SessionVisualizedMessage")), null, 2);
    }

    public static toSessionsUpdatedMessage(json: string): SessionsUpdatedMessage {
        return cast(JSON.parse(json), r("SessionsUpdatedMessage"));
    }

    public static sessionsUpdatedMessageToJson(value: SessionsUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("SessionsUpdatedMessage")), null, 2);
    }

    public static toSetSettingMessage(json: string): SetSettingMessage {
        return cast(JSON.parse(json), r("SetSettingMessage"));
    }

    public static setSettingMessageToJson(value: SetSettingMessage): string {
        return JSON.stringify(uncast(value, r("SetSettingMessage")), null, 2);
    }

    public static toSettingsUpdatedMessage(json: string): SettingsUpdatedMessage {
        return cast(JSON.parse(json), r("SettingsUpdatedMessage"));
    }

    public static settingsUpdatedMessageToJson(value: SettingsUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("SettingsUpdatedMessage")), null, 2);
    }

    public static toSpawnResultMessage(json: string): SpawnResultMessage {
        return cast(JSON.parse(json), r("SpawnResultMessage"));
    }

    public static spawnResultMessageToJson(value: SpawnResultMessage): string {
        return JSON.stringify(uncast(value, r("SpawnResultMessage")), null, 2);
    }

    public static toSpawnSessionMessage(json: string): SpawnSessionMessage {
        return cast(JSON.parse(json), r("SpawnSessionMessage"));
    }

    public static spawnSessionMessageToJson(value: SpawnSessionMessage): string {
        return JSON.stringify(uncast(value, r("SpawnSessionMessage")), null, 2);
    }

    public static toStartReviewMessage(json: string): StartReviewMessage {
        return cast(JSON.parse(json), r("StartReviewMessage"));
    }

    public static startReviewMessageToJson(value: StartReviewMessage): string {
        return JSON.stringify(uncast(value, r("StartReviewMessage")), null, 2);
    }

    public static toStashMessage(json: string): StashMessage {
        return cast(JSON.parse(json), r("StashMessage"));
    }

    public static stashMessageToJson(value: StashMessage): string {
        return JSON.stringify(uncast(value, r("StashMessage")), null, 2);
    }

    public static toStashPopMessage(json: string): StashPopMessage {
        return cast(JSON.parse(json), r("StashPopMessage"));
    }

    public static stashPopMessageToJson(value: StashPopMessage): string {
        return JSON.stringify(uncast(value, r("StashPopMessage")), null, 2);
    }

    public static toStashPopResultMessage(json: string): StashPopResultMessage {
        return cast(JSON.parse(json), r("StashPopResultMessage"));
    }

    public static stashPopResultMessageToJson(value: StashPopResultMessage): string {
        return JSON.stringify(uncast(value, r("StashPopResultMessage")), null, 2);
    }

    public static toStashResultMessage(json: string): StashResultMessage {
        return cast(JSON.parse(json), r("StashResultMessage"));
    }

    public static stashResultMessageToJson(value: StashResultMessage): string {
        return JSON.stringify(uncast(value, r("StashResultMessage")), null, 2);
    }

    public static toStateMessage(json: string): StateMessage {
        return cast(JSON.parse(json), r("StateMessage"));
    }

    public static stateMessageToJson(value: StateMessage): string {
        return JSON.stringify(uncast(value, r("StateMessage")), null, 2);
    }

    public static toStopMessage(json: string): StopMessage {
        return cast(JSON.parse(json), r("StopMessage"));
    }

    public static stopMessageToJson(value: StopMessage): string {
        return JSON.stringify(uncast(value, r("StopMessage")), null, 2);
    }

    public static toSubscribeGitStatusMessage(json: string): SubscribeGitStatusMessage {
        return cast(JSON.parse(json), r("SubscribeGitStatusMessage"));
    }

    public static subscribeGitStatusMessageToJson(value: SubscribeGitStatusMessage): string {
        return JSON.stringify(uncast(value, r("SubscribeGitStatusMessage")), null, 2);
    }

    public static toSwitchBranchMessage(json: string): SwitchBranchMessage {
        return cast(JSON.parse(json), r("SwitchBranchMessage"));
    }

    public static switchBranchMessageToJson(value: SwitchBranchMessage): string {
        return JSON.stringify(uncast(value, r("SwitchBranchMessage")), null, 2);
    }

    public static toSwitchBranchResultMessage(json: string): SwitchBranchResultMessage {
        return cast(JSON.parse(json), r("SwitchBranchResultMessage"));
    }

    public static switchBranchResultMessageToJson(value: SwitchBranchResultMessage): string {
        return JSON.stringify(uncast(value, r("SwitchBranchResultMessage")), null, 2);
    }

    public static toTodosMessage(json: string): TodosMessage {
        return cast(JSON.parse(json), r("TodosMessage"));
    }

    public static todosMessageToJson(value: TodosMessage): string {
        return JSON.stringify(uncast(value, r("TodosMessage")), null, 2);
    }

    public static toUnregisterMessage(json: string): UnregisterMessage {
        return cast(JSON.parse(json), r("UnregisterMessage"));
    }

    public static unregisterMessageToJson(value: UnregisterMessage): string {
        return JSON.stringify(uncast(value, r("UnregisterMessage")), null, 2);
    }

    public static toUnsubscribeGitStatusMessage(json: string): UnsubscribeGitStatusMessage {
        return cast(JSON.parse(json), r("UnsubscribeGitStatusMessage"));
    }

    public static unsubscribeGitStatusMessageToJson(value: UnsubscribeGitStatusMessage): string {
        return JSON.stringify(uncast(value, r("UnsubscribeGitStatusMessage")), null, 2);
    }

    public static toUpdateCommentMessage(json: string): UpdateCommentMessage {
        return cast(JSON.parse(json), r("UpdateCommentMessage"));
    }

    public static updateCommentMessageToJson(value: UpdateCommentMessage): string {
        return JSON.stringify(uncast(value, r("UpdateCommentMessage")), null, 2);
    }

    public static toUpdateCommentResultMessage(json: string): UpdateCommentResultMessage {
        return cast(JSON.parse(json), r("UpdateCommentResultMessage"));
    }

    public static updateCommentResultMessageToJson(value: UpdateCommentResultMessage): string {
        return JSON.stringify(uncast(value, r("UpdateCommentResultMessage")), null, 2);
    }

    public static toWebSocketEvent(json: string): WebSocketEvent {
        return cast(JSON.parse(json), r("WebSocketEvent"));
    }

    public static webSocketEventToJson(value: WebSocketEvent): string {
        return JSON.stringify(uncast(value, r("WebSocketEvent")), null, 2);
    }

    public static toWontFixCommentMessage(json: string): WontFixCommentMessage {
        return cast(JSON.parse(json), r("WontFixCommentMessage"));
    }

    public static wontFixCommentMessageToJson(value: WontFixCommentMessage): string {
        return JSON.stringify(uncast(value, r("WontFixCommentMessage")), null, 2);
    }

    public static toWontFixCommentResultMessage(json: string): WontFixCommentResultMessage {
        return cast(JSON.parse(json), r("WontFixCommentResultMessage"));
    }

    public static wontFixCommentResultMessageToJson(value: WontFixCommentResultMessage): string {
        return JSON.stringify(uncast(value, r("WontFixCommentResultMessage")), null, 2);
    }

    public static toWorktree(json: string): Worktree {
        return cast(JSON.parse(json), r("Worktree"));
    }

    public static worktreeToJson(value: Worktree): string {
        return JSON.stringify(uncast(value, r("Worktree")), null, 2);
    }

    public static toWorktreeCreatedEvent(json: string): WorktreeCreatedEvent {
        return cast(JSON.parse(json), r("WorktreeCreatedEvent"));
    }

    public static worktreeCreatedEventToJson(value: WorktreeCreatedEvent): string {
        return JSON.stringify(uncast(value, r("WorktreeCreatedEvent")), null, 2);
    }

    public static toWorktreeDeletedEvent(json: string): WorktreeDeletedEvent {
        return cast(JSON.parse(json), r("WorktreeDeletedEvent"));
    }

    public static worktreeDeletedEventToJson(value: WorktreeDeletedEvent): string {
        return JSON.stringify(uncast(value, r("WorktreeDeletedEvent")), null, 2);
    }

    public static toWorktreesUpdatedMessage(json: string): WorktreesUpdatedMessage {
        return cast(JSON.parse(json), r("WorktreesUpdatedMessage"));
    }

    public static worktreesUpdatedMessageToJson(value: WorktreesUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("WorktreesUpdatedMessage")), null, 2);
    }
}

function invalidValue(typ: any, val: any, key: any, parent: any = ''): never {
    const prettyTyp = prettyTypeName(typ);
    const parentText = parent ? ` on ${parent}` : '';
    const keyText = key ? ` for key "${key}"` : '';
    throw Error(`Invalid value${keyText}${parentText}. Expected ${prettyTyp} but got ${JSON.stringify(val)}`);
}

function prettyTypeName(typ: any): string {
    if (Array.isArray(typ)) {
        if (typ.length === 2 && typ[0] === undefined) {
            return `an optional ${prettyTypeName(typ[1])}`;
        } else {
            return `one of [${typ.map(a => { return prettyTypeName(a); }).join(", ")}]`;
        }
    } else if (typeof typ === "object" && typ.literal !== undefined) {
        return typ.literal;
    } else {
        return typeof typ;
    }
}

function jsonToJSProps(typ: any): any {
    if (typ.jsonToJS === undefined) {
        const map: any = {};
        typ.props.forEach((p: any) => map[p.json] = { key: p.js, typ: p.typ });
        typ.jsonToJS = map;
    }
    return typ.jsonToJS;
}

function jsToJSONProps(typ: any): any {
    if (typ.jsToJSON === undefined) {
        const map: any = {};
        typ.props.forEach((p: any) => map[p.js] = { key: p.json, typ: p.typ });
        typ.jsToJSON = map;
    }
    return typ.jsToJSON;
}

function transform(val: any, typ: any, getProps: any, key: any = '', parent: any = ''): any {
    function transformPrimitive(typ: string, val: any): any {
        if (typeof typ === typeof val) return val;
        return invalidValue(typ, val, key, parent);
    }

    function transformUnion(typs: any[], val: any): any {
        // val must validate against one typ in typs
        const l = typs.length;
        for (let i = 0; i < l; i++) {
            const typ = typs[i];
            try {
                return transform(val, typ, getProps);
            } catch (_) {}
        }
        return invalidValue(typs, val, key, parent);
    }

    function transformEnum(cases: string[], val: any): any {
        if (cases.indexOf(val) !== -1) return val;
        return invalidValue(cases.map(a => { return l(a); }), val, key, parent);
    }

    function transformArray(typ: any, val: any): any {
        // val must be an array with no invalid elements
        if (!Array.isArray(val)) return invalidValue(l("array"), val, key, parent);
        return val.map(el => transform(el, typ, getProps));
    }

    function transformDate(val: any): any {
        if (val === null) {
            return null;
        }
        const d = new Date(val);
        if (isNaN(d.valueOf())) {
            return invalidValue(l("Date"), val, key, parent);
        }
        return d;
    }

    function transformObject(props: { [k: string]: any }, additional: any, val: any): any {
        if (val === null || typeof val !== "object" || Array.isArray(val)) {
            return invalidValue(l(ref || "object"), val, key, parent);
        }
        const result: any = {};
        Object.getOwnPropertyNames(props).forEach(key => {
            const prop = props[key];
            const v = Object.prototype.hasOwnProperty.call(val, key) ? val[key] : undefined;
            result[prop.key] = transform(v, prop.typ, getProps, key, ref);
        });
        Object.getOwnPropertyNames(val).forEach(key => {
            if (!Object.prototype.hasOwnProperty.call(props, key)) {
                result[key] = transform(val[key], additional, getProps, key, ref);
            }
        });
        return result;
    }

    if (typ === "any") return val;
    if (typ === null) {
        if (val === null) return val;
        return invalidValue(typ, val, key, parent);
    }
    if (typ === false) return invalidValue(typ, val, key, parent);
    let ref: any = undefined;
    while (typeof typ === "object" && typ.ref !== undefined) {
        ref = typ.ref;
        typ = typeMap[typ.ref];
    }
    if (Array.isArray(typ)) return transformEnum(typ, val);
    if (typeof typ === "object") {
        return typ.hasOwnProperty("unionMembers") ? transformUnion(typ.unionMembers, val)
            : typ.hasOwnProperty("arrayItems")    ? transformArray(typ.arrayItems, val)
            : typ.hasOwnProperty("props")         ? transformObject(getProps(typ), typ.additional, val)
            : invalidValue(typ, val, key, parent);
    }
    // Numbers can be parsed by Date but shouldn't be.
    if (typ === Date && typeof val !== "number") return transformDate(val);
    return transformPrimitive(typ, val);
}

function cast<T>(val: any, typ: any): T {
    return transform(val, typ, jsonToJSProps);
}

function uncast<T>(val: T, typ: any): any {
    return transform(val, typ, jsToJSONProps);
}

function l(typ: any) {
    return { literal: typ };
}

function a(typ: any) {
    return { arrayItems: typ };
}

function u(...typs: any[]) {
    return { unionMembers: typs };
}

function o(props: any[], additional: any) {
    return { props, additional };
}

function m(additional: any) {
    return { props: [], additional };
}

function r(name: string) {
    return { ref: name };
}

const typeMap: any = {
    "AddCommentMessage": o([
        { json: "cmd", js: "cmd", typ: r("AddCommentMessageCmd") },
        { json: "content", js: "content", typ: "" },
        { json: "filepath", js: "filepath", typ: "" },
        { json: "line_end", js: "line_end", typ: 0 },
        { json: "line_start", js: "line_start", typ: 0 },
        { json: "review_id", js: "review_id", typ: "" },
    ], "any"),
    "AddCommentResultMessage": o([
        { json: "comment", js: "comment", typ: u(undefined, r("Comment")) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("AddCommentResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "Comment": o([
        { json: "author", js: "author", typ: "" },
        { json: "content", js: "content", typ: "" },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "filepath", js: "filepath", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "line_end", js: "line_end", typ: 0 },
        { json: "line_start", js: "line_start", typ: 0 },
        { json: "resolved", js: "resolved", typ: true },
        { json: "resolved_at", js: "resolved_at", typ: u(undefined, "") },
        { json: "resolved_by", js: "resolved_by", typ: u(undefined, "") },
        { json: "review_id", js: "review_id", typ: "" },
        { json: "wont_fix", js: "wont_fix", typ: true },
        { json: "wont_fix_at", js: "wont_fix_at", typ: u(undefined, "") },
        { json: "wont_fix_by", js: "wont_fix_by", typ: u(undefined, "") },
    ], "any"),
    "ApprovePRMessage": o([
        { json: "cmd", js: "cmd", typ: r("ApprovePRMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "AttachResultMessage": o([
        { json: "cols", js: "cols", typ: u(undefined, 0) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("AttachResultMessageEvent") },
        { json: "id", js: "id", typ: "" },
        { json: "last_seq", js: "last_seq", typ: u(undefined, 0) },
        { json: "pid", js: "pid", typ: u(undefined, 0) },
        { json: "rows", js: "rows", typ: u(undefined, 0) },
        { json: "running", js: "running", typ: u(undefined, true) },
        { json: "screen_cols", js: "screen_cols", typ: u(undefined, 0) },
        { json: "screen_cursor_visible", js: "screen_cursor_visible", typ: u(undefined, true) },
        { json: "screen_cursor_x", js: "screen_cursor_x", typ: u(undefined, 0) },
        { json: "screen_cursor_y", js: "screen_cursor_y", typ: u(undefined, 0) },
        { json: "screen_rows", js: "screen_rows", typ: u(undefined, 0) },
        { json: "screen_snapshot", js: "screen_snapshot", typ: u(undefined, "") },
        { json: "screen_snapshot_fresh", js: "screen_snapshot_fresh", typ: u(undefined, true) },
        { json: "scrollback", js: "scrollback", typ: u(undefined, "") },
        { json: "scrollback_truncated", js: "scrollback_truncated", typ: u(undefined, true) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "AttachSessionMessage": o([
        { json: "cmd", js: "cmd", typ: r("AttachSessionMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "AuthorState": o([
        { json: "author", js: "author", typ: "" },
        { json: "muted", js: "muted", typ: true },
    ], "any"),
    "AuthorsUpdatedMessage": o([
        { json: "authors", js: "authors", typ: u(undefined, a(r("AuthorElement"))) },
        { json: "event", js: "event", typ: r("AuthorsUpdatedMessageEvent") },
    ], "any"),
    "AuthorElement": o([
        { json: "author", js: "author", typ: "" },
        { json: "muted", js: "muted", typ: true },
    ], "any"),
    "Branch": o([
        { json: "commit_hash", js: "commit_hash", typ: u(undefined, "") },
        { json: "commit_time", js: "commit_time", typ: u(undefined, "") },
        { json: "is_current", js: "is_current", typ: u(undefined, true) },
        { json: "name", js: "name", typ: "" },
    ], "any"),
    "BranchChangedMessage": o([
        { json: "event", js: "event", typ: r("BranchChangedMessageEvent") },
        { json: "session", js: "session", typ: u(undefined, r("SessionElement")) },
    ], "any"),
    "SessionElement": o([
        { json: "agent", js: "agent", typ: r("SessionAgent") },
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "directory", js: "directory", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "is_worktree", js: "is_worktree", typ: u(undefined, true) },
        { json: "label", js: "label", typ: "" },
        { json: "last_seen", js: "last_seen", typ: "" },
        { json: "main_repo", js: "main_repo", typ: u(undefined, "") },
        { json: "muted", js: "muted", typ: true },
        { json: "needs_review_after_long_run", js: "needs_review_after_long_run", typ: u(undefined, true) },
        { json: "state", js: "state", typ: r("SessionState") },
        { json: "state_since", js: "state_since", typ: "" },
        { json: "state_updated_at", js: "state_updated_at", typ: "" },
        { json: "todos", js: "todos", typ: u(undefined, a("")) },
    ], "any"),
    "BranchDiffFile": o([
        { json: "additions", js: "additions", typ: u(undefined, 0) },
        { json: "deletions", js: "deletions", typ: u(undefined, 0) },
        { json: "has_uncommitted", js: "has_uncommitted", typ: u(undefined, true) },
        { json: "old_path", js: "old_path", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
        { json: "status", js: "status", typ: "" },
    ], "any"),
    "BranchDiffFilesResultMessage": o([
        { json: "base_ref", js: "base_ref", typ: "" },
        { json: "directory", js: "directory", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("BranchDiffFilesResultMessageEvent") },
        { json: "files", js: "files", typ: a(r("FileElement")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "FileElement": o([
        { json: "additions", js: "additions", typ: u(undefined, 0) },
        { json: "deletions", js: "deletions", typ: u(undefined, 0) },
        { json: "has_uncommitted", js: "has_uncommitted", typ: u(undefined, true) },
        { json: "old_path", js: "old_path", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
        { json: "status", js: "status", typ: "" },
    ], "any"),
    "BranchesResultMessage": o([
        { json: "branches", js: "branches", typ: a(r("BranchElement")) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("BranchesResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "BranchElement": o([
        { json: "commit_hash", js: "commit_hash", typ: u(undefined, "") },
        { json: "commit_time", js: "commit_time", typ: u(undefined, "") },
        { json: "is_current", js: "is_current", typ: u(undefined, true) },
        { json: "name", js: "name", typ: "" },
    ], "any"),
    "CancelReviewMessage": o([
        { json: "cmd", js: "cmd", typ: r("CancelReviewMessageCmd") },
        { json: "review_id", js: "review_id", typ: "" },
    ], "any"),
    "CheckAttnStashMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "cmd", js: "cmd", typ: r("CheckAttnStashMessageCmd") },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "CheckAttnStashResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("CheckAttnStashResultMessageEvent") },
        { json: "found", js: "found", typ: true },
        { json: "stash_ref", js: "stash_ref", typ: u(undefined, "") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "CheckDirtyMessage": o([
        { json: "cmd", js: "cmd", typ: r("CheckDirtyMessageCmd") },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "CheckDirtyResultMessage": o([
        { json: "dirty", js: "dirty", typ: true },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("CheckDirtyResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "ClearSessionsMessage": o([
        { json: "cmd", js: "cmd", typ: r("ClearSessionsMessageCmd") },
    ], "any"),
    "ClearWarningsMessage": o([
        { json: "cmd", js: "cmd", typ: r("ClearWarningsMessageCmd") },
    ], "any"),
    "CollapseRepoMessage": o([
        { json: "cmd", js: "cmd", typ: r("CollapseRepoMessageCmd") },
        { json: "collapsed", js: "collapsed", typ: true },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "CommandErrorMessage": o([
        { json: "cmd", js: "cmd", typ: u(undefined, "") },
        { json: "error", js: "error", typ: "" },
        { json: "event", js: "event", typ: r("CommandErrorMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "CommitWIPMessage": o([
        { json: "cmd", js: "cmd", typ: r("CommitWIPMessageCmd") },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "CommitWIPResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("CommitWIPResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "CreateBranchMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "cmd", js: "cmd", typ: r("CreateBranchMessageCmd") },
        { json: "main_repo", js: "main_repo", typ: "" },
    ], "any"),
    "CreateBranchResultMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("CreateBranchResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "CreateWorktreeFromBranchMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "cmd", js: "cmd", typ: r("CreateWorktreeFromBranchMessageCmd") },
        { json: "main_repo", js: "main_repo", typ: "" },
        { json: "path", js: "path", typ: u(undefined, "") },
    ], "any"),
    "CreateWorktreeMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "cmd", js: "cmd", typ: r("CreateWorktreeMessageCmd") },
        { json: "main_repo", js: "main_repo", typ: "" },
        { json: "path", js: "path", typ: u(undefined, "") },
        { json: "starting_from", js: "starting_from", typ: u(undefined, "") },
    ], "any"),
    "CreateWorktreeResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("CreateWorktreeResultMessageEvent") },
        { json: "path", js: "path", typ: u(undefined, "") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "DaemonWarning": o([
        { json: "code", js: "code", typ: "" },
        { json: "message", js: "message", typ: "" },
    ], "any"),
    "DeleteBranchMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "cmd", js: "cmd", typ: r("DeleteBranchMessageCmd") },
        { json: "force", js: "force", typ: true },
        { json: "main_repo", js: "main_repo", typ: "" },
    ], "any"),
    "DeleteBranchResultMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("DeleteBranchResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "DeleteCommentMessage": o([
        { json: "cmd", js: "cmd", typ: r("DeleteCommentMessageCmd") },
        { json: "comment_id", js: "comment_id", typ: "" },
    ], "any"),
    "DeleteCommentResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("DeleteCommentResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "DeleteWorktreeMessage": o([
        { json: "cmd", js: "cmd", typ: r("DeleteWorktreeMessageCmd") },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "DeleteWorktreeResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("DeleteWorktreeResultMessageEvent") },
        { json: "path", js: "path", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "DetachSessionMessage": o([
        { json: "cmd", js: "cmd", typ: r("DetachSessionMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "EnsureRepoMessage": o([
        { json: "clone_url", js: "clone_url", typ: "" },
        { json: "cmd", js: "cmd", typ: r("EnsureRepoMessageCmd") },
        { json: "target_path", js: "target_path", typ: "" },
    ], "any"),
    "EnsureRepoResultMessage": o([
        { json: "cloned", js: "cloned", typ: u(undefined, true) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("EnsureRepoResultMessageEvent") },
        { json: "success", js: "success", typ: u(undefined, true) },
        { json: "target_path", js: "target_path", typ: u(undefined, "") },
    ], "any"),
    "FetchPRDetailsMessage": o([
        { json: "cmd", js: "cmd", typ: r("FetchPRDetailsMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "FetchPRDetailsResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("FetchPRDetailsResultMessageEvent") },
        { json: "prs", js: "prs", typ: u(undefined, a(r("PRElement"))) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "PRElement": o([
        { json: "approved_by_me", js: "approved_by_me", typ: true },
        { json: "author", js: "author", typ: "" },
        { json: "ci_status", js: "ci_status", typ: u(undefined, "") },
        { json: "comment_count", js: "comment_count", typ: u(undefined, 0) },
        { json: "details_fetched", js: "details_fetched", typ: true },
        { json: "details_fetched_at", js: "details_fetched_at", typ: u(undefined, "") },
        { json: "has_new_changes", js: "has_new_changes", typ: true },
        { json: "head_branch", js: "head_branch", typ: u(undefined, "") },
        { json: "head_sha", js: "head_sha", typ: u(undefined, "") },
        { json: "heat_state", js: "heat_state", typ: u(undefined, r("HeatState")) },
        { json: "host", js: "host", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "last_heat_activity_at", js: "last_heat_activity_at", typ: u(undefined, "") },
        { json: "last_polled", js: "last_polled", typ: "" },
        { json: "last_updated", js: "last_updated", typ: "" },
        { json: "mergeable", js: "mergeable", typ: u(undefined, true) },
        { json: "mergeable_state", js: "mergeable_state", typ: u(undefined, "") },
        { json: "muted", js: "muted", typ: true },
        { json: "number", js: "number", typ: 0 },
        { json: "reason", js: "reason", typ: "" },
        { json: "repo", js: "repo", typ: "" },
        { json: "review_status", js: "review_status", typ: u(undefined, "") },
        { json: "role", js: "role", typ: r("PRRole") },
        { json: "state", js: "state", typ: "" },
        { json: "title", js: "title", typ: "" },
        { json: "url", js: "url", typ: "" },
    ], "any"),
    "FetchRemotesMessage": o([
        { json: "cmd", js: "cmd", typ: r("FetchRemotesMessageCmd") },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "FetchRemotesResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("FetchRemotesResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "FileDiffResultMessage": o([
        { json: "directory", js: "directory", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("FileDiffResultMessageEvent") },
        { json: "modified", js: "modified", typ: "" },
        { json: "original", js: "original", typ: "" },
        { json: "path", js: "path", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "GetBranchDiffFilesMessage": o([
        { json: "base_ref", js: "base_ref", typ: u(undefined, "") },
        { json: "cmd", js: "cmd", typ: r("GetBranchDiffFilesMessageCmd") },
        { json: "directory", js: "directory", typ: "" },
    ], "any"),
    "GetCommentsMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetCommentsMessageCmd") },
        { json: "filepath", js: "filepath", typ: u(undefined, "") },
        { json: "review_id", js: "review_id", typ: "" },
    ], "any"),
    "GetCommentsResultMessage": o([
        { json: "comments", js: "comments", typ: u(undefined, a(r("Comment"))) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("GetCommentsResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "GetDefaultBranchMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetDefaultBranchMessageCmd") },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "GetDefaultBranchResultMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("GetDefaultBranchResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "GetFileDiffMessage": o([
        { json: "base_ref", js: "base_ref", typ: u(undefined, "") },
        { json: "cmd", js: "cmd", typ: r("GetFileDiffMessageCmd") },
        { json: "directory", js: "directory", typ: "" },
        { json: "path", js: "path", typ: "" },
        { json: "staged", js: "staged", typ: u(undefined, true) },
    ], "any"),
    "GetRecentLocationsMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetRecentLocationsMessageCmd") },
        { json: "limit", js: "limit", typ: u(undefined, 0) },
    ], "any"),
    "GetRepoInfoMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetRepoInfoMessageCmd") },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "GetRepoInfoResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("GetRepoInfoResultMessageEvent") },
        { json: "info", js: "info", typ: u(undefined, r("Info")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "Info": o([
        { json: "branches", js: "branches", typ: a(r("BranchElement")) },
        { json: "current_branch", js: "current_branch", typ: "" },
        { json: "current_commit_hash", js: "current_commit_hash", typ: "" },
        { json: "current_commit_time", js: "current_commit_time", typ: "" },
        { json: "default_branch", js: "default_branch", typ: "" },
        { json: "fetched_at", js: "fetched_at", typ: u(undefined, "") },
        { json: "repo", js: "repo", typ: "" },
        { json: "worktrees", js: "worktrees", typ: a(r("WorktreeElement")) },
    ], "any"),
    "WorktreeElement": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "created_at", js: "created_at", typ: u(undefined, "") },
        { json: "main_repo", js: "main_repo", typ: "" },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "GetReviewStateMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "cmd", js: "cmd", typ: r("GetReviewStateMessageCmd") },
        { json: "repo_path", js: "repo_path", typ: "" },
    ], "any"),
    "GetReviewStateResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("GetReviewStateResultMessageEvent") },
        { json: "state", js: "state", typ: u(undefined, r("State")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "State": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "repo_path", js: "repo_path", typ: "" },
        { json: "review_id", js: "review_id", typ: "" },
        { json: "viewed_files", js: "viewed_files", typ: a("") },
    ], "any"),
    "GetSettingsMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetSettingsMessageCmd") },
    ], "any"),
    "GitFileChange": o([
        { json: "additions", js: "additions", typ: u(undefined, 0) },
        { json: "deletions", js: "deletions", typ: u(undefined, 0) },
        { json: "old_path", js: "old_path", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
        { json: "status", js: "status", typ: "" },
    ], "any"),
    "GitStatusUpdateMessage": o([
        { json: "directory", js: "directory", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("GitStatusUpdateMessageEvent") },
        { json: "staged", js: "staged", typ: a(r("StagedElement")) },
        { json: "unstaged", js: "unstaged", typ: a(r("StagedElement")) },
        { json: "untracked", js: "untracked", typ: a(r("StagedElement")) },
    ], "any"),
    "StagedElement": o([
        { json: "additions", js: "additions", typ: u(undefined, 0) },
        { json: "deletions", js: "deletions", typ: u(undefined, 0) },
        { json: "old_path", js: "old_path", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
        { json: "status", js: "status", typ: "" },
    ], "any"),
    "HeartbeatMessage": o([
        { json: "cmd", js: "cmd", typ: r("HeartbeatMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "InitialStateMessage": o([
        { json: "authors", js: "authors", typ: u(undefined, a(r("AuthorElement"))) },
        { json: "daemon_instance_id", js: "daemon_instance_id", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("InitialStateMessageEvent") },
        { json: "protocol_version", js: "protocol_version", typ: u(undefined, "") },
        { json: "prs", js: "prs", typ: u(undefined, a(r("PRElement"))) },
        { json: "repos", js: "repos", typ: u(undefined, a(r("RepoElement"))) },
        { json: "sessions", js: "sessions", typ: u(undefined, a(r("SessionElement"))) },
        { json: "settings", js: "settings", typ: u(undefined, m("any")) },
        { json: "warnings", js: "warnings", typ: u(undefined, a(r("WarningElement"))) },
    ], "any"),
    "RepoElement": o([
        { json: "collapsed", js: "collapsed", typ: true },
        { json: "muted", js: "muted", typ: true },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "WarningElement": o([
        { json: "code", js: "code", typ: "" },
        { json: "message", js: "message", typ: "" },
    ], "any"),
    "InjectTestPRMessage": o([
        { json: "cmd", js: "cmd", typ: r("InjectTestPRMessageCmd") },
        { json: "pr", js: "pr", typ: r("PRElement") },
    ], "any"),
    "InjectTestSessionMessage": o([
        { json: "cmd", js: "cmd", typ: r("InjectTestSessionMessageCmd") },
        { json: "session", js: "session", typ: r("SessionElement") },
    ], "any"),
    "KillSessionMessage": o([
        { json: "cmd", js: "cmd", typ: r("KillSessionMessageCmd") },
        { json: "id", js: "id", typ: "" },
        { json: "signal", js: "signal", typ: u(undefined, "") },
    ], "any"),
    "ListBranchesMessage": o([
        { json: "cmd", js: "cmd", typ: r("ListBranchesMessageCmd") },
        { json: "main_repo", js: "main_repo", typ: "" },
    ], "any"),
    "ListRemoteBranchesMessage": o([
        { json: "cmd", js: "cmd", typ: r("ListRemoteBranchesMessageCmd") },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "ListRemoteBranchesResultMessage": o([
        { json: "branches", js: "branches", typ: a(r("BranchElement")) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("ListRemoteBranchesResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "ListWorktreesMessage": o([
        { json: "cmd", js: "cmd", typ: r("ListWorktreesMessageCmd") },
        { json: "main_repo", js: "main_repo", typ: "" },
    ], "any"),
    "MarkFileViewedMessage": o([
        { json: "cmd", js: "cmd", typ: r("MarkFileViewedMessageCmd") },
        { json: "filepath", js: "filepath", typ: "" },
        { json: "review_id", js: "review_id", typ: "" },
        { json: "viewed", js: "viewed", typ: true },
    ], "any"),
    "MarkFileViewedResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("MarkFileViewedResultMessageEvent") },
        { json: "filepath", js: "filepath", typ: "" },
        { json: "review_id", js: "review_id", typ: "" },
        { json: "success", js: "success", typ: true },
        { json: "viewed", js: "viewed", typ: true },
    ], "any"),
    "MergePRMessage": o([
        { json: "cmd", js: "cmd", typ: r("MergePRMessageCmd") },
        { json: "id", js: "id", typ: "" },
        { json: "method", js: "method", typ: "" },
    ], "any"),
    "MuteAuthorMessage": o([
        { json: "author", js: "author", typ: "" },
        { json: "cmd", js: "cmd", typ: r("MuteAuthorMessageCmd") },
    ], "any"),
    "MuteMessage": o([
        { json: "cmd", js: "cmd", typ: r("MuteMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "MutePRMessage": o([
        { json: "cmd", js: "cmd", typ: r("MutePRMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "MuteRepoMessage": o([
        { json: "cmd", js: "cmd", typ: r("MuteRepoMessageCmd") },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "PR": o([
        { json: "approved_by_me", js: "approved_by_me", typ: true },
        { json: "author", js: "author", typ: "" },
        { json: "ci_status", js: "ci_status", typ: u(undefined, "") },
        { json: "comment_count", js: "comment_count", typ: u(undefined, 0) },
        { json: "details_fetched", js: "details_fetched", typ: true },
        { json: "details_fetched_at", js: "details_fetched_at", typ: u(undefined, "") },
        { json: "has_new_changes", js: "has_new_changes", typ: true },
        { json: "head_branch", js: "head_branch", typ: u(undefined, "") },
        { json: "head_sha", js: "head_sha", typ: u(undefined, "") },
        { json: "heat_state", js: "heat_state", typ: u(undefined, r("HeatState")) },
        { json: "host", js: "host", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "last_heat_activity_at", js: "last_heat_activity_at", typ: u(undefined, "") },
        { json: "last_polled", js: "last_polled", typ: "" },
        { json: "last_updated", js: "last_updated", typ: "" },
        { json: "mergeable", js: "mergeable", typ: u(undefined, true) },
        { json: "mergeable_state", js: "mergeable_state", typ: u(undefined, "") },
        { json: "muted", js: "muted", typ: true },
        { json: "number", js: "number", typ: 0 },
        { json: "reason", js: "reason", typ: "" },
        { json: "repo", js: "repo", typ: "" },
        { json: "review_status", js: "review_status", typ: u(undefined, "") },
        { json: "role", js: "role", typ: r("PRRole") },
        { json: "state", js: "state", typ: "" },
        { json: "title", js: "title", typ: "" },
        { json: "url", js: "url", typ: "" },
    ], "any"),
    "PRActionResultMessage": o([
        { json: "action", js: "action", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("PRActionResultMessageEvent") },
        { json: "id", js: "id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "PRVisitedMessage": o([
        { json: "cmd", js: "cmd", typ: r("PRVisitedMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "PRsUpdatedMessage": o([
        { json: "event", js: "event", typ: r("PRsUpdatedMessageEvent") },
        { json: "prs", js: "prs", typ: u(undefined, a(r("PRElement"))) },
    ], "any"),
    "PtyDesyncMessage": o([
        { json: "event", js: "event", typ: r("PtyDesyncMessageEvent") },
        { json: "id", js: "id", typ: "" },
        { json: "reason", js: "reason", typ: "" },
    ], "any"),
    "PtyInputMessage": o([
        { json: "cmd", js: "cmd", typ: r("PtyInputMessageCmd") },
        { json: "data", js: "data", typ: "" },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "PtyOutputMessage": o([
        { json: "data", js: "data", typ: "" },
        { json: "event", js: "event", typ: r("PtyOutputMessageEvent") },
        { json: "id", js: "id", typ: "" },
        { json: "seq", js: "seq", typ: 0 },
    ], "any"),
    "PtyResizeMessage": o([
        { json: "cmd", js: "cmd", typ: r("PtyResizeMessageCmd") },
        { json: "cols", js: "cols", typ: 0 },
        { json: "id", js: "id", typ: "" },
        { json: "rows", js: "rows", typ: 0 },
    ], "any"),
    "QueryAuthorsMessage": o([
        { json: "cmd", js: "cmd", typ: r("QueryAuthorsMessageCmd") },
    ], "any"),
    "QueryMessage": o([
        { json: "cmd", js: "cmd", typ: r("QueryMessageCmd") },
        { json: "filter", js: "filter", typ: u(undefined, "") },
    ], "any"),
    "QueryPRsMessage": o([
        { json: "cmd", js: "cmd", typ: r("QueryPRsMessageCmd") },
        { json: "filter", js: "filter", typ: u(undefined, "") },
    ], "any"),
    "QueryReposMessage": o([
        { json: "cmd", js: "cmd", typ: r("QueryReposMessageCmd") },
        { json: "filter", js: "filter", typ: u(undefined, "") },
    ], "any"),
    "RateLimitedMessage": o([
        { json: "event", js: "event", typ: r("RateLimitedMessageEvent") },
        { json: "rate_limit_reset_at", js: "rate_limit_reset_at", typ: "" },
        { json: "rate_limit_resource", js: "rate_limit_resource", typ: "" },
    ], "any"),
    "RecentLocation": o([
        { json: "label", js: "label", typ: "" },
        { json: "last_seen", js: "last_seen", typ: "" },
        { json: "path", js: "path", typ: "" },
        { json: "use_count", js: "use_count", typ: 0 },
    ], "any"),
    "RecentLocationsResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("RecentLocationsResultMessageEvent") },
        { json: "recent_locations", js: "recent_locations", typ: a(r("RecentLocationElement")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "RecentLocationElement": o([
        { json: "label", js: "label", typ: "" },
        { json: "last_seen", js: "last_seen", typ: "" },
        { json: "path", js: "path", typ: "" },
        { json: "use_count", js: "use_count", typ: 0 },
    ], "any"),
    "RefreshPRsMessage": o([
        { json: "cmd", js: "cmd", typ: r("RefreshPRsMessageCmd") },
    ], "any"),
    "RefreshPRsResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("RefreshPRsResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "RegisterMessage": o([
        { json: "agent", js: "agent", typ: u(undefined, r("SessionAgent")) },
        { json: "cmd", js: "cmd", typ: r("RegisterMessageCmd") },
        { json: "dir", js: "dir", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "label", js: "label", typ: u(undefined, "") },
    ], "any"),
    "RepoInfo": o([
        { json: "branches", js: "branches", typ: a(r("BranchElement")) },
        { json: "current_branch", js: "current_branch", typ: "" },
        { json: "current_commit_hash", js: "current_commit_hash", typ: "" },
        { json: "current_commit_time", js: "current_commit_time", typ: "" },
        { json: "default_branch", js: "default_branch", typ: "" },
        { json: "fetched_at", js: "fetched_at", typ: u(undefined, "") },
        { json: "repo", js: "repo", typ: "" },
        { json: "worktrees", js: "worktrees", typ: a(r("WorktreeElement")) },
    ], "any"),
    "RepoState": o([
        { json: "collapsed", js: "collapsed", typ: true },
        { json: "muted", js: "muted", typ: true },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "ReposUpdatedMessage": o([
        { json: "event", js: "event", typ: r("ReposUpdatedMessageEvent") },
        { json: "repos", js: "repos", typ: u(undefined, a(r("RepoElement"))) },
    ], "any"),
    "ResolveCommentMessage": o([
        { json: "cmd", js: "cmd", typ: r("ResolveCommentMessageCmd") },
        { json: "comment_id", js: "comment_id", typ: "" },
        { json: "resolved", js: "resolved", typ: true },
    ], "any"),
    "ResolveCommentResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("ResolveCommentResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "Response": o([
        { json: "authors", js: "authors", typ: u(undefined, a(r("AuthorElement"))) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "ok", js: "ok", typ: true },
        { json: "prs", js: "prs", typ: u(undefined, a(r("PRElement"))) },
        { json: "repos", js: "repos", typ: u(undefined, a(r("RepoElement"))) },
        { json: "sessions", js: "sessions", typ: u(undefined, a(r("SessionElement"))) },
    ], "any"),
    "ReviewCancelledMessage": o([
        { json: "event", js: "event", typ: r("ReviewCancelledMessageEvent") },
        { json: "review_id", js: "review_id", typ: "" },
    ], "any"),
    "ReviewChunkMessage": o([
        { json: "content", js: "content", typ: "" },
        { json: "event", js: "event", typ: r("ReviewChunkMessageEvent") },
        { json: "review_id", js: "review_id", typ: "" },
    ], "any"),
    "ReviewComment": o([
        { json: "author", js: "author", typ: "" },
        { json: "content", js: "content", typ: "" },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "filepath", js: "filepath", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "line_end", js: "line_end", typ: 0 },
        { json: "line_start", js: "line_start", typ: 0 },
        { json: "resolved", js: "resolved", typ: true },
        { json: "resolved_at", js: "resolved_at", typ: u(undefined, "") },
        { json: "resolved_by", js: "resolved_by", typ: u(undefined, "") },
        { json: "review_id", js: "review_id", typ: "" },
        { json: "wont_fix", js: "wont_fix", typ: true },
        { json: "wont_fix_at", js: "wont_fix_at", typ: u(undefined, "") },
        { json: "wont_fix_by", js: "wont_fix_by", typ: u(undefined, "") },
    ], "any"),
    "ReviewCommentResolvedMessage": o([
        { json: "comment_id", js: "comment_id", typ: "" },
        { json: "event", js: "event", typ: r("ReviewCommentResolvedMessageEvent") },
        { json: "review_id", js: "review_id", typ: "" },
    ], "any"),
    "ReviewCompleteMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("ReviewCompleteMessageEvent") },
        { json: "review_id", js: "review_id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "ReviewFinding": o([
        { json: "content", js: "content", typ: "" },
        { json: "filepath", js: "filepath", typ: "" },
        { json: "line_end", js: "line_end", typ: 0 },
        { json: "line_start", js: "line_start", typ: 0 },
        { json: "severity", js: "severity", typ: u(undefined, "") },
    ], "any"),
    "ReviewFindingMessage": o([
        { json: "comment", js: "comment", typ: u(undefined, r("Comment")) },
        { json: "event", js: "event", typ: r("ReviewFindingMessageEvent") },
        { json: "finding", js: "finding", typ: r("Finding") },
        { json: "review_id", js: "review_id", typ: "" },
    ], "any"),
    "Finding": o([
        { json: "content", js: "content", typ: "" },
        { json: "filepath", js: "filepath", typ: "" },
        { json: "line_end", js: "line_end", typ: 0 },
        { json: "line_start", js: "line_start", typ: 0 },
        { json: "severity", js: "severity", typ: u(undefined, "") },
    ], "any"),
    "ReviewStartedMessage": o([
        { json: "event", js: "event", typ: r("ReviewStartedMessageEvent") },
        { json: "review_id", js: "review_id", typ: "" },
    ], "any"),
    "ReviewState": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "repo_path", js: "repo_path", typ: "" },
        { json: "review_id", js: "review_id", typ: "" },
        { json: "viewed_files", js: "viewed_files", typ: a("") },
    ], "any"),
    "ReviewToolUse": o([
        { json: "input", js: "input", typ: m("any") },
        { json: "name", js: "name", typ: "" },
        { json: "output", js: "output", typ: "" },
    ], "any"),
    "ReviewToolUseMessage": o([
        { json: "event", js: "event", typ: r("ReviewToolUseMessageEvent") },
        { json: "review_id", js: "review_id", typ: "" },
        { json: "tool_use", js: "tool_use", typ: r("ToolUse") },
    ], "any"),
    "ToolUse": o([
        { json: "input", js: "input", typ: m("any") },
        { json: "name", js: "name", typ: "" },
        { json: "output", js: "output", typ: "" },
    ], "any"),
    "Session": o([
        { json: "agent", js: "agent", typ: r("SessionAgent") },
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "directory", js: "directory", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "is_worktree", js: "is_worktree", typ: u(undefined, true) },
        { json: "label", js: "label", typ: "" },
        { json: "last_seen", js: "last_seen", typ: "" },
        { json: "main_repo", js: "main_repo", typ: u(undefined, "") },
        { json: "muted", js: "muted", typ: true },
        { json: "needs_review_after_long_run", js: "needs_review_after_long_run", typ: u(undefined, true) },
        { json: "state", js: "state", typ: r("SessionState") },
        { json: "state_since", js: "state_since", typ: "" },
        { json: "state_updated_at", js: "state_updated_at", typ: "" },
        { json: "todos", js: "todos", typ: u(undefined, a("")) },
    ], "any"),
    "SessionExitedMessage": o([
        { json: "event", js: "event", typ: r("SessionExitedMessageEvent") },
        { json: "exit_code", js: "exit_code", typ: 0 },
        { json: "id", js: "id", typ: "" },
        { json: "signal", js: "signal", typ: u(undefined, "") },
    ], "any"),
    "SessionRegisteredMessage": o([
        { json: "event", js: "event", typ: r("SessionRegisteredMessageEvent") },
        { json: "session", js: "session", typ: r("SessionElement") },
    ], "any"),
    "SessionStateChangedMessage": o([
        { json: "event", js: "event", typ: r("SessionStateChangedMessageEvent") },
        { json: "session", js: "session", typ: r("SessionElement") },
    ], "any"),
    "SessionTodosUpdatedMessage": o([
        { json: "event", js: "event", typ: r("SessionTodosUpdatedMessageEvent") },
        { json: "session", js: "session", typ: r("SessionElement") },
    ], "any"),
    "SessionUnregisteredMessage": o([
        { json: "event", js: "event", typ: r("SessionUnregisteredMessageEvent") },
        { json: "session", js: "session", typ: r("SessionElement") },
    ], "any"),
    "SessionVisualizedMessage": o([
        { json: "cmd", js: "cmd", typ: r("SessionVisualizedMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "SessionsUpdatedMessage": o([
        { json: "event", js: "event", typ: r("SessionsUpdatedMessageEvent") },
        { json: "sessions", js: "sessions", typ: u(undefined, a(r("SessionElement"))) },
    ], "any"),
    "SetSettingMessage": o([
        { json: "cmd", js: "cmd", typ: r("SetSettingMessageCmd") },
        { json: "key", js: "key", typ: "" },
        { json: "value", js: "value", typ: "" },
    ], "any"),
    "SettingsUpdatedMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("SettingsUpdatedMessageEvent") },
        { json: "settings", js: "settings", typ: u(undefined, m("any")) },
        { json: "success", js: "success", typ: u(undefined, true) },
    ], "any"),
    "SpawnResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("SpawnResultMessageEvent") },
        { json: "id", js: "id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "SpawnSessionMessage": o([
        { json: "agent", js: "agent", typ: "" },
        { json: "claude_executable", js: "claude_executable", typ: u(undefined, "") },
        { json: "cmd", js: "cmd", typ: r("SpawnSessionMessageCmd") },
        { json: "codex_executable", js: "codex_executable", typ: u(undefined, "") },
        { json: "cols", js: "cols", typ: 0 },
        { json: "copilot_executable", js: "copilot_executable", typ: u(undefined, "") },
        { json: "cwd", js: "cwd", typ: "" },
        { json: "fork_session", js: "fork_session", typ: u(undefined, true) },
        { json: "id", js: "id", typ: "" },
        { json: "label", js: "label", typ: u(undefined, "") },
        { json: "resume_picker", js: "resume_picker", typ: u(undefined, true) },
        { json: "resume_session_id", js: "resume_session_id", typ: u(undefined, "") },
        { json: "rows", js: "rows", typ: 0 },
    ], "any"),
    "StartReviewMessage": o([
        { json: "base_branch", js: "base_branch", typ: "" },
        { json: "branch", js: "branch", typ: "" },
        { json: "cmd", js: "cmd", typ: r("StartReviewMessageCmd") },
        { json: "repo_path", js: "repo_path", typ: "" },
        { json: "review_id", js: "review_id", typ: "" },
    ], "any"),
    "StashMessage": o([
        { json: "cmd", js: "cmd", typ: r("StashMessageCmd") },
        { json: "message", js: "message", typ: "" },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "StashPopMessage": o([
        { json: "cmd", js: "cmd", typ: r("StashPopMessageCmd") },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "StashPopResultMessage": o([
        { json: "conflict", js: "conflict", typ: u(undefined, true) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("StashPopResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "StashResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("StashResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "StateMessage": o([
        { json: "cmd", js: "cmd", typ: r("StateMessageCmd") },
        { json: "id", js: "id", typ: "" },
        { json: "state", js: "state", typ: "" },
    ], "any"),
    "StopMessage": o([
        { json: "cmd", js: "cmd", typ: r("StopMessageCmd") },
        { json: "id", js: "id", typ: "" },
        { json: "transcript_path", js: "transcript_path", typ: "" },
    ], "any"),
    "SubscribeGitStatusMessage": o([
        { json: "cmd", js: "cmd", typ: r("SubscribeGitStatusMessageCmd") },
        { json: "directory", js: "directory", typ: "" },
    ], "any"),
    "SwitchBranchMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "cmd", js: "cmd", typ: r("SwitchBranchMessageCmd") },
        { json: "main_repo", js: "main_repo", typ: "" },
    ], "any"),
    "SwitchBranchResultMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("SwitchBranchResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "TodosMessage": o([
        { json: "cmd", js: "cmd", typ: r("TodosMessageCmd") },
        { json: "id", js: "id", typ: "" },
        { json: "todos", js: "todos", typ: a("") },
    ], "any"),
    "UnregisterMessage": o([
        { json: "cmd", js: "cmd", typ: r("UnregisterMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "UnsubscribeGitStatusMessage": o([
        { json: "cmd", js: "cmd", typ: r("UnsubscribeGitStatusMessageCmd") },
    ], "any"),
    "UpdateCommentMessage": o([
        { json: "cmd", js: "cmd", typ: r("UpdateCommentMessageCmd") },
        { json: "comment_id", js: "comment_id", typ: "" },
        { json: "content", js: "content", typ: "" },
    ], "any"),
    "UpdateCommentResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("UpdateCommentResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "WebSocketEvent": o([
        { json: "action", js: "action", typ: u(undefined, "") },
        { json: "authors", js: "authors", typ: u(undefined, a(r("AuthorElement"))) },
        { json: "base_ref", js: "base_ref", typ: u(undefined, "") },
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "branches", js: "branches", typ: u(undefined, a(r("BranchElement"))) },
        { json: "cloned", js: "cloned", typ: u(undefined, true) },
        { json: "cmd", js: "cmd", typ: u(undefined, "") },
        { json: "cols", js: "cols", typ: u(undefined, 0) },
        { json: "comment", js: "comment", typ: u(undefined, r("Comment")) },
        { json: "comment_id", js: "comment_id", typ: u(undefined, "") },
        { json: "conflict", js: "conflict", typ: u(undefined, true) },
        { json: "content", js: "content", typ: u(undefined, "") },
        { json: "data", js: "data", typ: u(undefined, "") },
        { json: "directory", js: "directory", typ: u(undefined, "") },
        { json: "dirty", js: "dirty", typ: u(undefined, true) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: "" },
        { json: "exit_code", js: "exit_code", typ: u(undefined, 0) },
        { json: "files", js: "files", typ: u(undefined, a(r("FileElement"))) },
        { json: "finding", js: "finding", typ: u(undefined, r("Finding")) },
        { json: "found", js: "found", typ: u(undefined, true) },
        { json: "id", js: "id", typ: u(undefined, "") },
        { json: "last_seq", js: "last_seq", typ: u(undefined, 0) },
        { json: "modified", js: "modified", typ: u(undefined, "") },
        { json: "original", js: "original", typ: u(undefined, "") },
        { json: "path", js: "path", typ: u(undefined, "") },
        { json: "pid", js: "pid", typ: u(undefined, 0) },
        { json: "protocol_version", js: "protocol_version", typ: u(undefined, "") },
        { json: "prs", js: "prs", typ: u(undefined, a(r("PRElement"))) },
        { json: "rate_limit_reset_at", js: "rate_limit_reset_at", typ: u(undefined, "") },
        { json: "rate_limit_resource", js: "rate_limit_resource", typ: u(undefined, "") },
        { json: "reason", js: "reason", typ: u(undefined, "") },
        { json: "recent_locations", js: "recent_locations", typ: u(undefined, a(r("RecentLocationElement"))) },
        { json: "repos", js: "repos", typ: u(undefined, a(r("RepoElement"))) },
        { json: "review_id", js: "review_id", typ: u(undefined, "") },
        { json: "rows", js: "rows", typ: u(undefined, 0) },
        { json: "running", js: "running", typ: u(undefined, true) },
        { json: "screen_cols", js: "screen_cols", typ: u(undefined, 0) },
        { json: "screen_cursor_visible", js: "screen_cursor_visible", typ: u(undefined, true) },
        { json: "screen_cursor_x", js: "screen_cursor_x", typ: u(undefined, 0) },
        { json: "screen_cursor_y", js: "screen_cursor_y", typ: u(undefined, 0) },
        { json: "screen_rows", js: "screen_rows", typ: u(undefined, 0) },
        { json: "screen_snapshot", js: "screen_snapshot", typ: u(undefined, "") },
        { json: "screen_snapshot_fresh", js: "screen_snapshot_fresh", typ: u(undefined, true) },
        { json: "scrollback", js: "scrollback", typ: u(undefined, "") },
        { json: "scrollback_truncated", js: "scrollback_truncated", typ: u(undefined, true) },
        { json: "seq", js: "seq", typ: u(undefined, 0) },
        { json: "session", js: "session", typ: u(undefined, r("SessionElement")) },
        { json: "sessions", js: "sessions", typ: u(undefined, a(r("SessionElement"))) },
        { json: "settings", js: "settings", typ: u(undefined, m("any")) },
        { json: "signal", js: "signal", typ: u(undefined, "") },
        { json: "staged", js: "staged", typ: u(undefined, a(r("StagedElement"))) },
        { json: "stash_ref", js: "stash_ref", typ: u(undefined, "") },
        { json: "success", js: "success", typ: u(undefined, true) },
        { json: "target_path", js: "target_path", typ: u(undefined, "") },
        { json: "tool_use", js: "tool_use", typ: u(undefined, r("ToolUse")) },
        { json: "unstaged", js: "unstaged", typ: u(undefined, a(r("StagedElement"))) },
        { json: "untracked", js: "untracked", typ: u(undefined, a(r("StagedElement"))) },
        { json: "warnings", js: "warnings", typ: u(undefined, a(r("WarningElement"))) },
        { json: "worktrees", js: "worktrees", typ: u(undefined, a(r("WorktreeElement"))) },
    ], "any"),
    "WontFixCommentMessage": o([
        { json: "cmd", js: "cmd", typ: r("WontFixCommentMessageCmd") },
        { json: "comment_id", js: "comment_id", typ: "" },
        { json: "wont_fix", js: "wont_fix", typ: true },
    ], "any"),
    "WontFixCommentResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("WontFixCommentResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "Worktree": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "created_at", js: "created_at", typ: u(undefined, "") },
        { json: "main_repo", js: "main_repo", typ: "" },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "WorktreeCreatedEvent": o([
        { json: "event", js: "event", typ: r("WorktreeCreatedEventEvent") },
        { json: "worktrees", js: "worktrees", typ: a(r("WorktreeElement")) },
    ], "any"),
    "WorktreeDeletedEvent": o([
        { json: "event", js: "event", typ: r("WorktreeDeletedEventEvent") },
        { json: "worktrees", js: "worktrees", typ: a(r("WorktreeElement")) },
    ], "any"),
    "WorktreesUpdatedMessage": o([
        { json: "event", js: "event", typ: r("WorktreesUpdatedMessageEvent") },
        { json: "worktrees", js: "worktrees", typ: u(undefined, a(r("WorktreeElement"))) },
    ], "any"),
    "AddCommentMessageCmd": [
        "add_comment",
    ],
    "AddCommentResultMessageEvent": [
        "add_comment_result",
    ],
    "ApprovePRMessageCmd": [
        "approve_pr",
    ],
    "AttachResultMessageEvent": [
        "attach_result",
    ],
    "AttachSessionMessageCmd": [
        "attach_session",
    ],
    "AuthorsUpdatedMessageEvent": [
        "authors_updated",
    ],
    "BranchChangedMessageEvent": [
        "branch_changed",
    ],
    "SessionAgent": [
        "claude",
        "codex",
        "copilot",
    ],
    "SessionState": [
        "idle",
        "launching",
        "pending_approval",
        "unknown",
        "waiting_input",
        "working",
    ],
    "BranchDiffFilesResultMessageEvent": [
        "branch_diff_files_result",
    ],
    "BranchesResultMessageEvent": [
        "branches_result",
    ],
    "CancelReviewMessageCmd": [
        "cancel_review",
    ],
    "CheckAttnStashMessageCmd": [
        "check_attn_stash",
    ],
    "CheckAttnStashResultMessageEvent": [
        "check_attn_stash_result",
    ],
    "CheckDirtyMessageCmd": [
        "check_dirty",
    ],
    "CheckDirtyResultMessageEvent": [
        "check_dirty_result",
    ],
    "ClearSessionsMessageCmd": [
        "clear_sessions",
    ],
    "ClearWarningsMessageCmd": [
        "clear_warnings",
    ],
    "CollapseRepoMessageCmd": [
        "collapse_repo",
    ],
    "CommandErrorMessageEvent": [
        "command_error",
    ],
    "CommitWIPMessageCmd": [
        "commit_wip",
    ],
    "CommitWIPResultMessageEvent": [
        "commit_wip_result",
    ],
    "CreateBranchMessageCmd": [
        "create_branch",
    ],
    "CreateBranchResultMessageEvent": [
        "create_branch_result",
    ],
    "CreateWorktreeFromBranchMessageCmd": [
        "create_worktree_from_branch",
    ],
    "CreateWorktreeMessageCmd": [
        "create_worktree",
    ],
    "CreateWorktreeResultMessageEvent": [
        "create_worktree_result",
    ],
    "DeleteBranchMessageCmd": [
        "delete_branch",
    ],
    "DeleteBranchResultMessageEvent": [
        "delete_branch_result",
    ],
    "DeleteCommentMessageCmd": [
        "delete_comment",
    ],
    "DeleteCommentResultMessageEvent": [
        "delete_comment_result",
    ],
    "DeleteWorktreeMessageCmd": [
        "delete_worktree",
    ],
    "DeleteWorktreeResultMessageEvent": [
        "delete_worktree_result",
    ],
    "DetachSessionMessageCmd": [
        "detach_session",
    ],
    "EnsureRepoMessageCmd": [
        "ensure_repo",
    ],
    "EnsureRepoResultMessageEvent": [
        "ensure_repo_result",
    ],
    "FetchPRDetailsMessageCmd": [
        "fetch_pr_details",
    ],
    "FetchPRDetailsResultMessageEvent": [
        "fetch_pr_details_result",
    ],
    "HeatState": [
        "cold",
        "hot",
        "warm",
    ],
    "PRRole": [
        "author",
        "reviewer",
    ],
    "FetchRemotesMessageCmd": [
        "fetch_remotes",
    ],
    "FetchRemotesResultMessageEvent": [
        "fetch_remotes_result",
    ],
    "FileDiffResultMessageEvent": [
        "file_diff_result",
    ],
    "GetBranchDiffFilesMessageCmd": [
        "get_branch_diff_files",
    ],
    "GetCommentsMessageCmd": [
        "get_comments",
    ],
    "GetCommentsResultMessageEvent": [
        "get_comments_result",
    ],
    "GetDefaultBranchMessageCmd": [
        "get_default_branch",
    ],
    "GetDefaultBranchResultMessageEvent": [
        "get_default_branch_result",
    ],
    "GetFileDiffMessageCmd": [
        "get_file_diff",
    ],
    "GetRecentLocationsMessageCmd": [
        "get_recent_locations",
    ],
    "GetRepoInfoMessageCmd": [
        "get_repo_info",
    ],
    "GetRepoInfoResultMessageEvent": [
        "get_repo_info_result",
    ],
    "GetReviewStateMessageCmd": [
        "get_review_state",
    ],
    "GetReviewStateResultMessageEvent": [
        "get_review_state_result",
    ],
    "GetSettingsMessageCmd": [
        "get_settings",
    ],
    "GitStatusUpdateMessageEvent": [
        "git_status_update",
    ],
    "HeartbeatMessageCmd": [
        "heartbeat",
    ],
    "InitialStateMessageEvent": [
        "initial_state",
    ],
    "InjectTestPRMessageCmd": [
        "inject_test_pr",
    ],
    "InjectTestSessionMessageCmd": [
        "inject_test_session",
    ],
    "KillSessionMessageCmd": [
        "kill_session",
    ],
    "ListBranchesMessageCmd": [
        "list_branches",
    ],
    "ListRemoteBranchesMessageCmd": [
        "list_remote_branches",
    ],
    "ListRemoteBranchesResultMessageEvent": [
        "list_remote_branches_result",
    ],
    "ListWorktreesMessageCmd": [
        "list_worktrees",
    ],
    "MarkFileViewedMessageCmd": [
        "mark_file_viewed",
    ],
    "MarkFileViewedResultMessageEvent": [
        "mark_file_viewed_result",
    ],
    "MergePRMessageCmd": [
        "merge_pr",
    ],
    "MuteAuthorMessageCmd": [
        "mute_author",
    ],
    "MuteMessageCmd": [
        "mute",
    ],
    "MutePRMessageCmd": [
        "mute_pr",
    ],
    "MuteRepoMessageCmd": [
        "mute_repo",
    ],
    "PRActionResultMessageEvent": [
        "pr_action_result",
    ],
    "PRVisitedMessageCmd": [
        "pr_visited",
    ],
    "PRsUpdatedMessageEvent": [
        "prs_updated",
    ],
    "PtyDesyncMessageEvent": [
        "pty_desync",
    ],
    "PtyInputMessageCmd": [
        "pty_input",
    ],
    "PtyOutputMessageEvent": [
        "pty_output",
    ],
    "PtyResizeMessageCmd": [
        "pty_resize",
    ],
    "QueryAuthorsMessageCmd": [
        "query_authors",
    ],
    "QueryMessageCmd": [
        "query",
    ],
    "QueryPRsMessageCmd": [
        "query_prs",
    ],
    "QueryReposMessageCmd": [
        "query_repos",
    ],
    "RateLimitedMessageEvent": [
        "rate_limited",
    ],
    "RecentLocationsResultMessageEvent": [
        "recent_locations_result",
    ],
    "RefreshPRsMessageCmd": [
        "refresh_prs",
    ],
    "RefreshPRsResultMessageEvent": [
        "refresh_prs_result",
    ],
    "RegisterMessageCmd": [
        "register",
    ],
    "ReposUpdatedMessageEvent": [
        "repos_updated",
    ],
    "ResolveCommentMessageCmd": [
        "resolve_comment",
    ],
    "ResolveCommentResultMessageEvent": [
        "resolve_comment_result",
    ],
    "ReviewCancelledMessageEvent": [
        "review_cancelled",
    ],
    "ReviewChunkMessageEvent": [
        "review_chunk",
    ],
    "ReviewCommentResolvedMessageEvent": [
        "review_comment_resolved",
    ],
    "ReviewCompleteMessageEvent": [
        "review_complete",
    ],
    "ReviewFindingMessageEvent": [
        "review_finding",
    ],
    "ReviewStartedMessageEvent": [
        "review_started",
    ],
    "ReviewToolUseMessageEvent": [
        "review_tool_use",
    ],
    "SessionExitedMessageEvent": [
        "session_exited",
    ],
    "SessionRegisteredMessageEvent": [
        "session_registered",
    ],
    "SessionStateChangedMessageEvent": [
        "session_state_changed",
    ],
    "SessionTodosUpdatedMessageEvent": [
        "session_todos_updated",
    ],
    "SessionUnregisteredMessageEvent": [
        "session_unregistered",
    ],
    "SessionVisualizedMessageCmd": [
        "session_visualized",
    ],
    "SessionsUpdatedMessageEvent": [
        "sessions_updated",
    ],
    "SetSettingMessageCmd": [
        "set_setting",
    ],
    "SettingsUpdatedMessageEvent": [
        "settings_updated",
    ],
    "SpawnResultMessageEvent": [
        "spawn_result",
    ],
    "SpawnSessionMessageCmd": [
        "spawn_session",
    ],
    "StartReviewMessageCmd": [
        "start_review",
    ],
    "StashMessageCmd": [
        "stash",
    ],
    "StashPopMessageCmd": [
        "stash_pop",
    ],
    "StashPopResultMessageEvent": [
        "stash_pop_result",
    ],
    "StashResultMessageEvent": [
        "stash_result",
    ],
    "StateMessageCmd": [
        "state",
    ],
    "StopMessageCmd": [
        "stop",
    ],
    "SubscribeGitStatusMessageCmd": [
        "subscribe_git_status",
    ],
    "SwitchBranchMessageCmd": [
        "switch_branch",
    ],
    "SwitchBranchResultMessageEvent": [
        "switch_branch_result",
    ],
    "TodosMessageCmd": [
        "todos",
    ],
    "UnregisterMessageCmd": [
        "unregister",
    ],
    "UnsubscribeGitStatusMessageCmd": [
        "unsubscribe_git_status",
    ],
    "UpdateCommentMessageCmd": [
        "update_comment",
    ],
    "UpdateCommentResultMessageEvent": [
        "update_comment_result",
    ],
    "WontFixCommentMessageCmd": [
        "wont_fix_comment",
    ],
    "WontFixCommentResultMessageEvent": [
        "wont_fix_comment_result",
    ],
    "WorktreeCreatedEventEvent": [
        "worktree_created",
    ],
    "WorktreeDeletedEventEvent": [
        "worktree_deleted",
    ],
    "WorktreesUpdatedMessageEvent": [
        "worktrees_updated",
    ],
};
