// To parse this data:
//
//   import { Convert, AcknowledgeDispatchMessage, AddCommentMessage, AddCommentResultMessage, AddEndpointMessage, AnswerReviewLoopMessage, ApprovePRMessage, AttachPolicy, AttachResultMessage, AttachSessionMessage, AuthorState, AuthorsUpdatedMessage, BootstrapEndpointMessage, Branch, BranchChangedMessage, BranchDiffFile, BranchDiffFilesResultMessage, BranchesResultMessage, BrowseDirectoryMessage, BrowseDirectoryResultMessage, BrowserControlMessage, BrowserControlRequestMessage, BrowserControlResponseMessage, BrowserControlResultMessage, ChiefOfStaffDispatch, ChiefOfStaffDispatchesUpdatedMessage, ChiefOfStaffResultMessage, ClearSessionsMessage, ClearWarningsMessage, ClientHelloMessage, CollapseRepoMessage, CommandErrorMessage, CreateWorktreeFromBranchMessage, CreateWorktreeMessage, CreateWorktreeResultMessage, DaemonWarning, DelegateMessage, DelegateResult, DelegateResultMessage, DelegateWorktreeRequest, DeleteCommentMessage, DeleteCommentResultMessage, DeleteWorktreeMessage, DeleteWorktreeResultMessage, DetachSessionMessage, DirectoryEntry, DispatchArtifact, DispatchDecisionRequest, DispatchMessage, DispatchReport, DispatchReportType, DispatchRequestStatus, DispatchVerification, DispatchWorkState, EndpointActionResultMessage, EndpointCapabilities, EndpointInfo, EndpointStatusChangedMessage, EndpointsUpdatedMessage, EnsureRepoMessage, EnsureRepoResultMessage, FetchPRDetailsMessage, FetchPRDetailsResultMessage, FetchRemotesMessage, FetchRemotesResultMessage, FileDiffResultMessage, GetBranchDiffFilesMessage, GetCommentsMessage, GetCommentsResultMessage, GetDefaultBranchMessage, GetDefaultBranchResultMessage, GetDispatchMessage, GetFileDiffMessage, GetRecentLocationsMessage, GetRepoInfoMessage, GetRepoInfoResultMessage, GetReviewLoopRunMessage, GetReviewLoopStateMessage, GetReviewStateMessage, GetReviewStateResultMessage, GetScreenSnapshotMessage, GetScreenSnapshotResultMessage, GetSettingsMessage, GitFileChange, GitHubHostsUpdatedMessage, GitOperation, GitOperationFinishedMessage, GitOperationKind, GitOperationStartedMessage, GitOperationStatus, GitStatusUpdateMessage, HeartbeatMessage, HeatState, InitialStateMessage, InjectTestPRMessage, InjectTestSessionMessage, InspectPathMessage, InspectPathResultMessage, InstallPluginMessage, KillSessionMessage, ListBranchesMessage, ListDispatchesMessage, ListDispatchMessagesMessage, ListEndpointsMessage, ListPluginsMessage, ListRemoteBranchesMessage, ListRemoteBranchesResultMessage, ListWorktreesMessage, MarkFileViewedMessage, MarkFileViewedResultMessage, MergePRMessage, MuteAuthorMessage, MutePRMessage, MuteRepoMessage, MuteWorkspaceMessage, OpenBrowserMessage, OpenMarkdownMessage, PathInspection, PluginActionResultMessage, PluginInfo, PluginIssue, PluginsUpdatedMessage, PR, PRActionResultMessage, PRRole, PRsUpdatedMessage, PRVisitedMessage, PtyDesyncMessage, PtyInputMessage, PtyOutputMessage, PtyResizedMessage, PtyResizeMessage, QueryAuthorsMessage, QueryMessage, QueryPRsMessage, QueryReposMessage, RateLimitedMessage, ReadDispatchMessage, RecentLocation, RecentLocationsResultMessage, RefreshPRsMessage, RefreshPRsResultMessage, RegisterMessage, RegisterWorkspaceMessage, RemoveEndpointMessage, RemovePluginMessage, RenameResultMessage, RenameSessionMessage, RenameWorkspaceMessage, ReplaySegment, RepoInfo, ReportDispatchMessage, RepoState, ReposUpdatedMessage, ResolveCommentMessage, ResolveCommentResultMessage, ResolveDispatchRequestMessage, Response, ReviewComment, ReviewLoopDecision, ReviewLoopInteraction, ReviewLoopInteractionStatus, ReviewLoopIteration, ReviewLoopIterationStatus, ReviewLoopResultMessage, ReviewLoopRun, ReviewLoopRunStatus, ReviewLoopState, ReviewLoopStatus, ReviewLoopUpdatedMessage, ReviewState, SendDispatchMessage, Session, SessionExitedMessage, SessionRegisteredMessage, SessionSelectedMessage, SessionState, SessionStateChangedMessage, SessionsUpdatedMessage, SessionTodosUpdatedMessage, SessionUnregisteredMessage, SessionVisualizedMessage, SetChiefOfStaffMessage, SetEndpointRemoteWebMessage, SetPluginPriorityMessage, SetReviewLoopIterationLimitMessage, SetSessionResumeIDMessage, SetSettingMessage, SettingsUpdatedMessage, SetWorkspaceRankMessage, SpawnResultMessage, SpawnSessionMessage, StartReviewLoopMessage, StateMessage, StopMessage, StopReviewLoopMessage, SubscribeGitStatusMessage, TodosMessage, UnregisterMessage, UnregisterWorkspaceMessage, UnsubscribeGitStatusMessage, UpdateCommentMessage, UpdateCommentResultMessage, UpdateEndpointMessage, WakeDispatchAgentMessage, WakeDispatchAgentResultMessage, WebSocketEvent, Workspace, WorkspaceContext, WorkspaceContextChangedMessage, WorkspaceContextCheckoutMessage, WorkspaceContextCompactMessage, WorkspaceContextListMessage, WorkspaceContextListResultMessage, WorkspaceContextMaintenanceAction, WorkspaceContextMaintenanceResult, WorkspaceContextResult, WorkspaceContextResultMessage, WorkspaceContextRollbackMessage, WorkspaceContextStatusMessage, WorkspaceContextUpdateMessage, WorkspaceLayout, WorkspaceLayoutActionResultMessage, WorkspaceLayoutAddSessionPaneMessage, WorkspaceLayoutClosePaneMessage, WorkspaceLayoutDockEdge, WorkspaceLayoutDockTileMessage, WorkspaceLayoutFocusPaneMessage, WorkspaceLayoutGetMessage, WorkspaceLayoutMessage, WorkspaceLayoutMoveLeafMessage, WorkspaceLayoutMoveLeafToNewWorkspaceMessage, WorkspaceLayoutMoveLeafToWorkspaceMessage, WorkspaceLayoutPane, WorkspaceLayoutPaneKind, WorkspaceLayoutPaneStatus, WorkspaceLayoutRenamePaneMessage, WorkspaceLayoutSetSplitRatioMessage, WorkspaceLayoutSplitDirection, WorkspaceLayoutUndockTileMessage, WorkspaceLayoutUpdatedMessage, WorkspaceLayoutUpdateTileMessage, WorkspaceRegisteredMessage, WorkspaceSelectedMessage, WorkspaceStateChangedMessage, WorkspaceStatus, WorkspaceTileContentGetMessage, WorkspaceTileContentMessage, WorkspaceUnregisteredMessage, Worktree, WorktreeCreatedEvent, WorktreeDeletedEvent, WorktreesUpdatedMessage } from "./file";
//
//   const acknowledgeDispatchMessage = Convert.toAcknowledgeDispatchMessage(json);
//   const addCommentMessage = Convert.toAddCommentMessage(json);
//   const addCommentResultMessage = Convert.toAddCommentResultMessage(json);
//   const addEndpointMessage = Convert.toAddEndpointMessage(json);
//   const answerReviewLoopMessage = Convert.toAnswerReviewLoopMessage(json);
//   const approvePRMessage = Convert.toApprovePRMessage(json);
//   const attachPolicy = Convert.toAttachPolicy(json);
//   const attachResultMessage = Convert.toAttachResultMessage(json);
//   const attachSessionMessage = Convert.toAttachSessionMessage(json);
//   const authorState = Convert.toAuthorState(json);
//   const authorsUpdatedMessage = Convert.toAuthorsUpdatedMessage(json);
//   const bootstrapEndpointMessage = Convert.toBootstrapEndpointMessage(json);
//   const branch = Convert.toBranch(json);
//   const branchChangedMessage = Convert.toBranchChangedMessage(json);
//   const branchDiffFile = Convert.toBranchDiffFile(json);
//   const branchDiffFilesResultMessage = Convert.toBranchDiffFilesResultMessage(json);
//   const branchesResultMessage = Convert.toBranchesResultMessage(json);
//   const browseDirectoryMessage = Convert.toBrowseDirectoryMessage(json);
//   const browseDirectoryResultMessage = Convert.toBrowseDirectoryResultMessage(json);
//   const browserControlMessage = Convert.toBrowserControlMessage(json);
//   const browserControlRequestMessage = Convert.toBrowserControlRequestMessage(json);
//   const browserControlResponseMessage = Convert.toBrowserControlResponseMessage(json);
//   const browserControlResultMessage = Convert.toBrowserControlResultMessage(json);
//   const chiefOfStaffDispatch = Convert.toChiefOfStaffDispatch(json);
//   const chiefOfStaffDispatchesUpdatedMessage = Convert.toChiefOfStaffDispatchesUpdatedMessage(json);
//   const chiefOfStaffResultMessage = Convert.toChiefOfStaffResultMessage(json);
//   const clearSessionsMessage = Convert.toClearSessionsMessage(json);
//   const clearWarningsMessage = Convert.toClearWarningsMessage(json);
//   const clientHelloMessage = Convert.toClientHelloMessage(json);
//   const collapseRepoMessage = Convert.toCollapseRepoMessage(json);
//   const commandErrorMessage = Convert.toCommandErrorMessage(json);
//   const createWorktreeFromBranchMessage = Convert.toCreateWorktreeFromBranchMessage(json);
//   const createWorktreeMessage = Convert.toCreateWorktreeMessage(json);
//   const createWorktreeResultMessage = Convert.toCreateWorktreeResultMessage(json);
//   const daemonWarning = Convert.toDaemonWarning(json);
//   const delegateMessage = Convert.toDelegateMessage(json);
//   const delegateResult = Convert.toDelegateResult(json);
//   const delegateResultMessage = Convert.toDelegateResultMessage(json);
//   const delegateWorktreeRequest = Convert.toDelegateWorktreeRequest(json);
//   const deleteCommentMessage = Convert.toDeleteCommentMessage(json);
//   const deleteCommentResultMessage = Convert.toDeleteCommentResultMessage(json);
//   const deleteWorktreeMessage = Convert.toDeleteWorktreeMessage(json);
//   const deleteWorktreeResultMessage = Convert.toDeleteWorktreeResultMessage(json);
//   const detachSessionMessage = Convert.toDetachSessionMessage(json);
//   const directoryEntry = Convert.toDirectoryEntry(json);
//   const dispatchArtifact = Convert.toDispatchArtifact(json);
//   const dispatchDecisionRequest = Convert.toDispatchDecisionRequest(json);
//   const dispatchMessage = Convert.toDispatchMessage(json);
//   const dispatchReport = Convert.toDispatchReport(json);
//   const dispatchReportType = Convert.toDispatchReportType(json);
//   const dispatchRequestStatus = Convert.toDispatchRequestStatus(json);
//   const dispatchVerification = Convert.toDispatchVerification(json);
//   const dispatchWorkState = Convert.toDispatchWorkState(json);
//   const endpointActionResultMessage = Convert.toEndpointActionResultMessage(json);
//   const endpointCapabilities = Convert.toEndpointCapabilities(json);
//   const endpointInfo = Convert.toEndpointInfo(json);
//   const endpointStatusChangedMessage = Convert.toEndpointStatusChangedMessage(json);
//   const endpointsUpdatedMessage = Convert.toEndpointsUpdatedMessage(json);
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
//   const getDispatchMessage = Convert.toGetDispatchMessage(json);
//   const getFileDiffMessage = Convert.toGetFileDiffMessage(json);
//   const getRecentLocationsMessage = Convert.toGetRecentLocationsMessage(json);
//   const getRepoInfoMessage = Convert.toGetRepoInfoMessage(json);
//   const getRepoInfoResultMessage = Convert.toGetRepoInfoResultMessage(json);
//   const getReviewLoopRunMessage = Convert.toGetReviewLoopRunMessage(json);
//   const getReviewLoopStateMessage = Convert.toGetReviewLoopStateMessage(json);
//   const getReviewStateMessage = Convert.toGetReviewStateMessage(json);
//   const getReviewStateResultMessage = Convert.toGetReviewStateResultMessage(json);
//   const getScreenSnapshotMessage = Convert.toGetScreenSnapshotMessage(json);
//   const getScreenSnapshotResultMessage = Convert.toGetScreenSnapshotResultMessage(json);
//   const getSettingsMessage = Convert.toGetSettingsMessage(json);
//   const gitFileChange = Convert.toGitFileChange(json);
//   const gitHubHostsUpdatedMessage = Convert.toGitHubHostsUpdatedMessage(json);
//   const gitOperation = Convert.toGitOperation(json);
//   const gitOperationFinishedMessage = Convert.toGitOperationFinishedMessage(json);
//   const gitOperationKind = Convert.toGitOperationKind(json);
//   const gitOperationStartedMessage = Convert.toGitOperationStartedMessage(json);
//   const gitOperationStatus = Convert.toGitOperationStatus(json);
//   const gitStatusUpdateMessage = Convert.toGitStatusUpdateMessage(json);
//   const heartbeatMessage = Convert.toHeartbeatMessage(json);
//   const heatState = Convert.toHeatState(json);
//   const initialStateMessage = Convert.toInitialStateMessage(json);
//   const injectTestPRMessage = Convert.toInjectTestPRMessage(json);
//   const injectTestSessionMessage = Convert.toInjectTestSessionMessage(json);
//   const inspectPathMessage = Convert.toInspectPathMessage(json);
//   const inspectPathResultMessage = Convert.toInspectPathResultMessage(json);
//   const installPluginMessage = Convert.toInstallPluginMessage(json);
//   const killSessionMessage = Convert.toKillSessionMessage(json);
//   const listBranchesMessage = Convert.toListBranchesMessage(json);
//   const listDispatchesMessage = Convert.toListDispatchesMessage(json);
//   const listDispatchMessagesMessage = Convert.toListDispatchMessagesMessage(json);
//   const listEndpointsMessage = Convert.toListEndpointsMessage(json);
//   const listPluginsMessage = Convert.toListPluginsMessage(json);
//   const listRemoteBranchesMessage = Convert.toListRemoteBranchesMessage(json);
//   const listRemoteBranchesResultMessage = Convert.toListRemoteBranchesResultMessage(json);
//   const listWorktreesMessage = Convert.toListWorktreesMessage(json);
//   const markFileViewedMessage = Convert.toMarkFileViewedMessage(json);
//   const markFileViewedResultMessage = Convert.toMarkFileViewedResultMessage(json);
//   const mergePRMessage = Convert.toMergePRMessage(json);
//   const muteAuthorMessage = Convert.toMuteAuthorMessage(json);
//   const mutePRMessage = Convert.toMutePRMessage(json);
//   const muteRepoMessage = Convert.toMuteRepoMessage(json);
//   const muteWorkspaceMessage = Convert.toMuteWorkspaceMessage(json);
//   const openBrowserMessage = Convert.toOpenBrowserMessage(json);
//   const openMarkdownMessage = Convert.toOpenMarkdownMessage(json);
//   const pathInspection = Convert.toPathInspection(json);
//   const pluginActionResultMessage = Convert.toPluginActionResultMessage(json);
//   const pluginInfo = Convert.toPluginInfo(json);
//   const pluginIssue = Convert.toPluginIssue(json);
//   const pluginsUpdatedMessage = Convert.toPluginsUpdatedMessage(json);
//   const pR = Convert.toPR(json);
//   const pRActionResultMessage = Convert.toPRActionResultMessage(json);
//   const pRRole = Convert.toPRRole(json);
//   const pRsUpdatedMessage = Convert.toPRsUpdatedMessage(json);
//   const pRVisitedMessage = Convert.toPRVisitedMessage(json);
//   const ptyDesyncMessage = Convert.toPtyDesyncMessage(json);
//   const ptyInputMessage = Convert.toPtyInputMessage(json);
//   const ptyOutputMessage = Convert.toPtyOutputMessage(json);
//   const ptyResizedMessage = Convert.toPtyResizedMessage(json);
//   const ptyResizeMessage = Convert.toPtyResizeMessage(json);
//   const queryAuthorsMessage = Convert.toQueryAuthorsMessage(json);
//   const queryMessage = Convert.toQueryMessage(json);
//   const queryPRsMessage = Convert.toQueryPRsMessage(json);
//   const queryReposMessage = Convert.toQueryReposMessage(json);
//   const rateLimitedMessage = Convert.toRateLimitedMessage(json);
//   const readDispatchMessage = Convert.toReadDispatchMessage(json);
//   const recentLocation = Convert.toRecentLocation(json);
//   const recentLocationsResultMessage = Convert.toRecentLocationsResultMessage(json);
//   const refreshPRsMessage = Convert.toRefreshPRsMessage(json);
//   const refreshPRsResultMessage = Convert.toRefreshPRsResultMessage(json);
//   const registerMessage = Convert.toRegisterMessage(json);
//   const registerWorkspaceMessage = Convert.toRegisterWorkspaceMessage(json);
//   const removeEndpointMessage = Convert.toRemoveEndpointMessage(json);
//   const removePluginMessage = Convert.toRemovePluginMessage(json);
//   const renameResultMessage = Convert.toRenameResultMessage(json);
//   const renameSessionMessage = Convert.toRenameSessionMessage(json);
//   const renameWorkspaceMessage = Convert.toRenameWorkspaceMessage(json);
//   const replaySegment = Convert.toReplaySegment(json);
//   const repoInfo = Convert.toRepoInfo(json);
//   const reportDispatchMessage = Convert.toReportDispatchMessage(json);
//   const repoState = Convert.toRepoState(json);
//   const reposUpdatedMessage = Convert.toReposUpdatedMessage(json);
//   const resolveCommentMessage = Convert.toResolveCommentMessage(json);
//   const resolveCommentResultMessage = Convert.toResolveCommentResultMessage(json);
//   const resolveDispatchRequestMessage = Convert.toResolveDispatchRequestMessage(json);
//   const response = Convert.toResponse(json);
//   const reviewComment = Convert.toReviewComment(json);
//   const reviewLoopDecision = Convert.toReviewLoopDecision(json);
//   const reviewLoopInteraction = Convert.toReviewLoopInteraction(json);
//   const reviewLoopInteractionStatus = Convert.toReviewLoopInteractionStatus(json);
//   const reviewLoopIteration = Convert.toReviewLoopIteration(json);
//   const reviewLoopIterationStatus = Convert.toReviewLoopIterationStatus(json);
//   const reviewLoopResultMessage = Convert.toReviewLoopResultMessage(json);
//   const reviewLoopRun = Convert.toReviewLoopRun(json);
//   const reviewLoopRunStatus = Convert.toReviewLoopRunStatus(json);
//   const reviewLoopState = Convert.toReviewLoopState(json);
//   const reviewLoopStatus = Convert.toReviewLoopStatus(json);
//   const reviewLoopUpdatedMessage = Convert.toReviewLoopUpdatedMessage(json);
//   const reviewState = Convert.toReviewState(json);
//   const sendDispatchMessage = Convert.toSendDispatchMessage(json);
//   const session = Convert.toSession(json);
//   const sessionExitedMessage = Convert.toSessionExitedMessage(json);
//   const sessionRegisteredMessage = Convert.toSessionRegisteredMessage(json);
//   const sessionSelectedMessage = Convert.toSessionSelectedMessage(json);
//   const sessionState = Convert.toSessionState(json);
//   const sessionStateChangedMessage = Convert.toSessionStateChangedMessage(json);
//   const sessionsUpdatedMessage = Convert.toSessionsUpdatedMessage(json);
//   const sessionTodosUpdatedMessage = Convert.toSessionTodosUpdatedMessage(json);
//   const sessionUnregisteredMessage = Convert.toSessionUnregisteredMessage(json);
//   const sessionVisualizedMessage = Convert.toSessionVisualizedMessage(json);
//   const setChiefOfStaffMessage = Convert.toSetChiefOfStaffMessage(json);
//   const setEndpointRemoteWebMessage = Convert.toSetEndpointRemoteWebMessage(json);
//   const setPluginPriorityMessage = Convert.toSetPluginPriorityMessage(json);
//   const setReviewLoopIterationLimitMessage = Convert.toSetReviewLoopIterationLimitMessage(json);
//   const setSessionResumeIDMessage = Convert.toSetSessionResumeIDMessage(json);
//   const setSettingMessage = Convert.toSetSettingMessage(json);
//   const settingsUpdatedMessage = Convert.toSettingsUpdatedMessage(json);
//   const setWorkspaceRankMessage = Convert.toSetWorkspaceRankMessage(json);
//   const spawnResultMessage = Convert.toSpawnResultMessage(json);
//   const spawnSessionMessage = Convert.toSpawnSessionMessage(json);
//   const startReviewLoopMessage = Convert.toStartReviewLoopMessage(json);
//   const stateMessage = Convert.toStateMessage(json);
//   const stopMessage = Convert.toStopMessage(json);
//   const stopReviewLoopMessage = Convert.toStopReviewLoopMessage(json);
//   const subscribeGitStatusMessage = Convert.toSubscribeGitStatusMessage(json);
//   const todosMessage = Convert.toTodosMessage(json);
//   const unregisterMessage = Convert.toUnregisterMessage(json);
//   const unregisterWorkspaceMessage = Convert.toUnregisterWorkspaceMessage(json);
//   const unsubscribeGitStatusMessage = Convert.toUnsubscribeGitStatusMessage(json);
//   const updateCommentMessage = Convert.toUpdateCommentMessage(json);
//   const updateCommentResultMessage = Convert.toUpdateCommentResultMessage(json);
//   const updateEndpointMessage = Convert.toUpdateEndpointMessage(json);
//   const wakeDispatchAgentMessage = Convert.toWakeDispatchAgentMessage(json);
//   const wakeDispatchAgentResultMessage = Convert.toWakeDispatchAgentResultMessage(json);
//   const webSocketEvent = Convert.toWebSocketEvent(json);
//   const workspace = Convert.toWorkspace(json);
//   const workspaceContext = Convert.toWorkspaceContext(json);
//   const workspaceContextChangedMessage = Convert.toWorkspaceContextChangedMessage(json);
//   const workspaceContextCheckoutMessage = Convert.toWorkspaceContextCheckoutMessage(json);
//   const workspaceContextCompactMessage = Convert.toWorkspaceContextCompactMessage(json);
//   const workspaceContextListMessage = Convert.toWorkspaceContextListMessage(json);
//   const workspaceContextListResultMessage = Convert.toWorkspaceContextListResultMessage(json);
//   const workspaceContextMaintenanceAction = Convert.toWorkspaceContextMaintenanceAction(json);
//   const workspaceContextMaintenanceResult = Convert.toWorkspaceContextMaintenanceResult(json);
//   const workspaceContextResult = Convert.toWorkspaceContextResult(json);
//   const workspaceContextResultMessage = Convert.toWorkspaceContextResultMessage(json);
//   const workspaceContextRollbackMessage = Convert.toWorkspaceContextRollbackMessage(json);
//   const workspaceContextStatusMessage = Convert.toWorkspaceContextStatusMessage(json);
//   const workspaceContextUpdateMessage = Convert.toWorkspaceContextUpdateMessage(json);
//   const workspaceLayout = Convert.toWorkspaceLayout(json);
//   const workspaceLayoutActionResultMessage = Convert.toWorkspaceLayoutActionResultMessage(json);
//   const workspaceLayoutAddSessionPaneMessage = Convert.toWorkspaceLayoutAddSessionPaneMessage(json);
//   const workspaceLayoutClosePaneMessage = Convert.toWorkspaceLayoutClosePaneMessage(json);
//   const workspaceLayoutDockEdge = Convert.toWorkspaceLayoutDockEdge(json);
//   const workspaceLayoutDockTileMessage = Convert.toWorkspaceLayoutDockTileMessage(json);
//   const workspaceLayoutFocusPaneMessage = Convert.toWorkspaceLayoutFocusPaneMessage(json);
//   const workspaceLayoutGetMessage = Convert.toWorkspaceLayoutGetMessage(json);
//   const workspaceLayoutMessage = Convert.toWorkspaceLayoutMessage(json);
//   const workspaceLayoutMoveLeafMessage = Convert.toWorkspaceLayoutMoveLeafMessage(json);
//   const workspaceLayoutMoveLeafToNewWorkspaceMessage = Convert.toWorkspaceLayoutMoveLeafToNewWorkspaceMessage(json);
//   const workspaceLayoutMoveLeafToWorkspaceMessage = Convert.toWorkspaceLayoutMoveLeafToWorkspaceMessage(json);
//   const workspaceLayoutPane = Convert.toWorkspaceLayoutPane(json);
//   const workspaceLayoutPaneKind = Convert.toWorkspaceLayoutPaneKind(json);
//   const workspaceLayoutPaneStatus = Convert.toWorkspaceLayoutPaneStatus(json);
//   const workspaceLayoutRenamePaneMessage = Convert.toWorkspaceLayoutRenamePaneMessage(json);
//   const workspaceLayoutSetSplitRatioMessage = Convert.toWorkspaceLayoutSetSplitRatioMessage(json);
//   const workspaceLayoutSplitDirection = Convert.toWorkspaceLayoutSplitDirection(json);
//   const workspaceLayoutUndockTileMessage = Convert.toWorkspaceLayoutUndockTileMessage(json);
//   const workspaceLayoutUpdatedMessage = Convert.toWorkspaceLayoutUpdatedMessage(json);
//   const workspaceLayoutUpdateTileMessage = Convert.toWorkspaceLayoutUpdateTileMessage(json);
//   const workspaceRegisteredMessage = Convert.toWorkspaceRegisteredMessage(json);
//   const workspaceSelectedMessage = Convert.toWorkspaceSelectedMessage(json);
//   const workspaceStateChangedMessage = Convert.toWorkspaceStateChangedMessage(json);
//   const workspaceStatus = Convert.toWorkspaceStatus(json);
//   const workspaceTileContentGetMessage = Convert.toWorkspaceTileContentGetMessage(json);
//   const workspaceTileContentMessage = Convert.toWorkspaceTileContentMessage(json);
//   const workspaceUnregisteredMessage = Convert.toWorkspaceUnregisteredMessage(json);
//   const worktree = Convert.toWorktree(json);
//   const worktreeCreatedEvent = Convert.toWorktreeCreatedEvent(json);
//   const worktreeDeletedEvent = Convert.toWorktreeDeletedEvent(json);
//   const worktreesUpdatedMessage = Convert.toWorktreesUpdatedMessage(json);
//
// These functions will throw an error if the JSON doesn't
// match the expected interface, even if the JSON is valid.

export interface AcknowledgeDispatchMessage {
    acknowledgement?:  string;
    cmd:               AcknowledgeDispatchMessageCmd;
    message_id:        string;
    source_session_id: string;
    [property: string]: any;
}

export enum AcknowledgeDispatchMessageCmd {
    AcknowledgeDispatchMessage = "acknowledge_dispatch_message",
}

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
    [property: string]: any;
}

export enum AddCommentResultMessageEvent {
    AddCommentResult = "add_comment_result",
}

export interface AddEndpointMessage {
    cmd:        AddEndpointMessageCmd;
    name:       string;
    profile?:   string;
    ssh_target: string;
    [property: string]: any;
}

export enum AddEndpointMessageCmd {
    AddEndpoint = "add_endpoint",
}

export interface AnswerReviewLoopMessage {
    answer:          string;
    cmd:             AnswerReviewLoopMessageCmd;
    interaction_id?: string;
    loop_id:         string;
    [property: string]: any;
}

export enum AnswerReviewLoopMessageCmd {
    AnswerReviewLoop = "answer_review_loop",
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
    replay_segments?:       ReplaySegmentElement[];
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

export interface ReplaySegmentElement {
    cols: number;
    data: string;
    rows: number;
    [property: string]: any;
}

export interface AttachSessionMessage {
    attach_policy?: AttachPolicy;
    cmd:            AttachSessionMessageCmd;
    id:             string;
    [property: string]: any;
}

export enum AttachPolicy {
    FreshSpawn = "fresh_spawn",
    RelaunchRestore = "relaunch_restore",
    SameAppRemount = "same_app_remount",
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

export interface BootstrapEndpointMessage {
    cmd:         BootstrapEndpointMessageCmd;
    endpoint_id: string;
    [property: string]: any;
}

export enum BootstrapEndpointMessageCmd {
    BootstrapEndpoint = "bootstrap_endpoint",
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
    agent:                        string;
    branch?:                      string;
    chief_of_staff?:              boolean;
    directory:                    string;
    endpoint_id?:                 string;
    id:                           string;
    is_worktree?:                 boolean;
    label:                        string;
    last_seen:                    string;
    main_repo?:                   string;
    needs_review_after_long_run?: boolean;
    recoverable?:                 boolean;
    state:                        WorkspaceStatus;
    state_since:                  string;
    state_updated_at:             string;
    todos?:                       string[];
    workspace_id:                 string;
    [property: string]: any;
}

export enum WorkspaceStatus {
    Idle = "idle",
    Launching = "launching",
    PendingApproval = "pending_approval",
    Scheduled = "scheduled",
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

export interface BrowseDirectoryMessage {
    cmd:          BrowseDirectoryMessageCmd;
    endpoint_id?: string;
    input_path:   string;
    request_id?:  string;
    [property: string]: any;
}

export enum BrowseDirectoryMessageCmd {
    BrowseDirectory = "browse_directory",
}

export interface BrowseDirectoryResultMessage {
    directory:    string;
    endpoint_id?: string;
    entries:      EntryElement[];
    error?:       string;
    event:        BrowseDirectoryResultMessageEvent;
    home_path?:   string;
    input_path:   string;
    request_id?:  string;
    success:      boolean;
    [property: string]: any;
}

export interface EntryElement {
    name: string;
    path: string;
    [property: string]: any;
}

export enum BrowseDirectoryResultMessageEvent {
    BrowseDirectoryResult = "browse_directory_result",
}

export interface BrowserControlMessage {
    action:        string;
    cmd:           BrowserControlMessageCmd;
    params?:       string;
    request_id?:   string;
    selector?:     string;
    session_id?:   string;
    text?:         string;
    workspace_id?: string;
    [property: string]: any;
}

export enum BrowserControlMessageCmd {
    BrowserControl = "browser_control",
}

export interface BrowserControlRequestMessage {
    action:       string;
    event:        BrowserControlRequestMessageEvent;
    params?:      string;
    request_id:   string;
    selector?:    string;
    text?:        string;
    tile_id:      string;
    workspace_id: string;
    [property: string]: any;
}

export enum BrowserControlRequestMessageEvent {
    BrowserControlRequest = "browser_control_request",
}

export interface BrowserControlResponseMessage {
    data?:      string;
    error?:     string;
    event:      BrowserControlResponseMessageEvent;
    request_id: string;
    success:    boolean;
    [property: string]: any;
}

export enum BrowserControlResponseMessageEvent {
    BrowserControlResponse = "browser_control_response",
}

export interface BrowserControlResultMessage {
    cmd:        BrowserControlResultMessageCmd;
    data?:      string;
    error?:     string;
    request_id: string;
    success:    boolean;
    [property: string]: any;
}

export enum BrowserControlResultMessageCmd {
    BrowserControlResult = "browser_control_result",
}

export interface ChiefOfStaffDispatch {
    actionable?:           boolean;
    agent:                 string;
    branch?:               string;
    brief:                 string;
    chief_session_id:      string;
    concise_summary?:      string;
    created_at:            string;
    directory:             string;
    id:                    string;
    label:                 string;
    latest_report?:        string;
    reported_at?:          string;
    session_id:            string;
    status:                string;
    status_since:          string;
    structured_report?:    Report;
    unread_message_count?: number;
    updated_at:            string;
    workspace_id:          string;
    [property: string]: any;
}

export interface Report {
    artifact?:        Artifact;
    constraints?:     string[];
    next_action?:     string;
    next_actor?:      string;
    remaining_scope?: string[];
    report_type:      DispatchReportType;
    reported_at:      string;
    request?:         Request;
    summary:          string;
    verification?:    VerificationElement[];
    work_state:       DispatchWorkState;
    [property: string]: any;
}

export interface Artifact {
    branch?:       string;
    description?:  string;
    dirty?:        boolean;
    identity:      string;
    revision?:     string;
    workspace_id?: string;
    [property: string]: any;
}

export enum DispatchReportType {
    Blocker = "blocker",
    Completion = "completion",
    Failure = "failure",
    Handoff = "handoff",
    Progress = "progress",
}

export interface Request {
    consequence?:       string;
    expected_responder: string;
    question:           string;
    recommendation?:    string;
    resolution_link?:   string;
    responded_at?:      string;
    responded_by?:      string;
    response?:          string;
    status:             DispatchRequestStatus;
    [property: string]: any;
}

export enum DispatchRequestStatus {
    Pending = "pending",
    Resolved = "resolved",
}

export interface VerificationElement {
    actor:             string;
    artifact_identity: string;
    current?:          boolean;
    result:            string;
    target:            string;
    timestamp:         string;
    [property: string]: any;
}

export enum DispatchWorkState {
    Completed = "completed",
    Failed = "failed",
    InProgress = "in_progress",
    NeedsInput = "needs_input",
    ReadyForReview = "ready_for_review",
}

export interface ChiefOfStaffDispatchesUpdatedMessage {
    dispatches: ChiefOfStaffDispatchElement[];
    event:      ChiefOfStaffDispatchesUpdatedMessageEvent;
    [property: string]: any;
}

export interface ChiefOfStaffDispatchElement {
    actionable?:           boolean;
    agent:                 string;
    branch?:               string;
    brief:                 string;
    chief_session_id:      string;
    concise_summary?:      string;
    created_at:            string;
    directory:             string;
    id:                    string;
    label:                 string;
    latest_report?:        string;
    reported_at?:          string;
    session_id:            string;
    status:                string;
    status_since:          string;
    structured_report?:    Report;
    unread_message_count?: number;
    updated_at:            string;
    workspace_id:          string;
    [property: string]: any;
}

export enum ChiefOfStaffDispatchesUpdatedMessageEvent {
    ChiefOfStaffDispatchesUpdated = "chief_of_staff_dispatches_updated",
}

export interface ChiefOfStaffResultMessage {
    chief_of_staff:       boolean;
    error?:               string;
    event:                ChiefOfStaffResultMessageEvent;
    previous_session_id?: string;
    session_id:           string;
    success:              boolean;
    [property: string]: any;
}

export enum ChiefOfStaffResultMessageEvent {
    ChiefOfStaffResult = "chief_of_staff_result",
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

export interface ClientHelloMessage {
    browser_host_token?: string;
    capabilities:        string[];
    client_kind:         string;
    cmd:                 ClientHelloMessageCmd;
    version:             string;
    [property: string]: any;
}

export enum ClientHelloMessageCmd {
    ClientHello = "client_hello",
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
    endpoint_id?:   string;
    main_repo:      string;
    path?:          string;
    starting_from?: string;
    [property: string]: any;
}

export enum CreateWorktreeMessageCmd {
    CreateWorktree = "create_worktree",
}

export interface CreateWorktreeResultMessage {
    endpoint_id?: string;
    error?:       string;
    event:        CreateWorktreeResultMessageEvent;
    path?:        string;
    success:      boolean;
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

export interface DelegateMessage {
    agent?:            string;
    brief:             string;
    cmd:               DelegateMessageCmd;
    cwd?:              string;
    label?:            string;
    placement?:        string;
    source_session_id: string;
    workspace_id?:     string;
    worktree?:         DelegateMessageWorktree;
    yolo_mode?:        boolean;
    [property: string]: any;
}

export enum DelegateMessageCmd {
    Delegate = "delegate",
}

export interface DelegateMessageWorktree {
    branch:         string;
    path?:          string;
    repo?:          string;
    starting_from?: string;
    [property: string]: any;
}

export interface DelegateResult {
    branch?:           string;
    directory:         string;
    dispatch_id?:      string;
    placement:         string;
    session_id:        string;
    workspace_id:      string;
    worktree_created?: boolean;
    [property: string]: any;
}

export interface DelegateResultMessage {
    error?:  string;
    event:   DelegateResultMessageEvent;
    result?: DelegateResultObject;
    success: boolean;
    [property: string]: any;
}

export enum DelegateResultMessageEvent {
    DelegateResult = "delegate_result",
}

export interface DelegateResultObject {
    branch?:           string;
    directory:         string;
    dispatch_id?:      string;
    placement:         string;
    session_id:        string;
    workspace_id:      string;
    worktree_created?: boolean;
    [property: string]: any;
}

export interface DelegateWorktreeRequest {
    branch:         string;
    path?:          string;
    repo?:          string;
    starting_from?: string;
    [property: string]: any;
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
    cmd:          GitOperationKind;
    endpoint_id?: string;
    force?:       boolean;
    path:         string;
    [property: string]: any;
}

export enum GitOperationKind {
    DeleteWorktree = "delete_worktree",
}

export interface DeleteWorktreeResultMessage {
    endpoint_id?: string;
    error?:       string;
    event:        DeleteWorktreeResultMessageEvent;
    forceable?:   boolean;
    path:         string;
    reason_kind?: ReasonKind;
    success:      boolean;
    [property: string]: any;
}

export enum DeleteWorktreeResultMessageEvent {
    DeleteWorktreeResult = "delete_worktree_result",
}

export enum ReasonKind {
    DirtyWorktree = "dirty_worktree",
    GitError = "git_error",
    NotFound = "not_found",
    ProviderError = "provider_error",
}

export interface DetachSessionMessage {
    cmd: DetachSessionMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum DetachSessionMessageCmd {
    DetachSession = "detach_session",
}

export interface DirectoryEntry {
    name: string;
    path: string;
    [property: string]: any;
}

export interface DispatchArtifact {
    branch?:       string;
    description?:  string;
    dirty?:        boolean;
    identity:      string;
    revision?:     string;
    workspace_id?: string;
    [property: string]: any;
}

export interface DispatchDecisionRequest {
    consequence?:       string;
    expected_responder: string;
    question:           string;
    recommendation?:    string;
    resolution_link?:   string;
    responded_at?:      string;
    responded_by?:      string;
    response?:          string;
    status:             DispatchRequestStatus;
    [property: string]: any;
}

export interface DispatchMessage {
    acknowledged_at?:  string;
    acknowledgement?:  string;
    content:           string;
    created_at:        string;
    dispatch_id:       string;
    id:                string;
    read_at?:          string;
    sender_session_id: string;
    target_session_id: string;
    [property: string]: any;
}

export interface DispatchReport {
    artifact?:        Artifact;
    constraints?:     string[];
    next_action?:     string;
    next_actor?:      string;
    remaining_scope?: string[];
    report_type:      DispatchReportType;
    reported_at:      string;
    request?:         Request;
    summary:          string;
    verification?:    VerificationElement[];
    work_state:       DispatchWorkState;
    [property: string]: any;
}

export interface DispatchVerification {
    actor:             string;
    artifact_identity: string;
    current?:          boolean;
    result:            string;
    target:            string;
    timestamp:         string;
    [property: string]: any;
}

export interface EndpointActionResultMessage {
    action:       string;
    endpoint_id?: string;
    error?:       string;
    event:        EndpointActionResultMessageEvent;
    success:      boolean;
    [property: string]: any;
}

export enum EndpointActionResultMessageEvent {
    EndpointActionResult = "endpoint_action_result",
}

export interface EndpointCapabilities {
    agents_available:    string[];
    daemon_instance_id?: string;
    projects_directory?: string;
    protocol_version:    string;
    pty_backend_mode?:   string;
    tailscale_auth_url?: string;
    tailscale_domain?:   string;
    tailscale_enabled?:  boolean;
    tailscale_error?:    string;
    tailscale_status?:   string;
    tailscale_url?:      string;
    [property: string]: any;
}

export interface EndpointInfo {
    capabilities?:   Capabilities;
    enabled?:        boolean;
    id:              string;
    name:            string;
    profile?:        string;
    session_count?:  number;
    ssh_target:      string;
    status:          string;
    status_message?: string;
    [property: string]: any;
}

export interface Capabilities {
    agents_available:    string[];
    daemon_instance_id?: string;
    projects_directory?: string;
    protocol_version:    string;
    pty_backend_mode?:   string;
    tailscale_auth_url?: string;
    tailscale_domain?:   string;
    tailscale_enabled?:  boolean;
    tailscale_error?:    string;
    tailscale_status?:   string;
    tailscale_url?:      string;
    [property: string]: any;
}

export interface EndpointStatusChangedMessage {
    endpoint: Endpoint;
    event:    EndpointStatusChangedMessageEvent;
    [property: string]: any;
}

export interface Endpoint {
    capabilities?:   Capabilities;
    enabled?:        boolean;
    id:              string;
    name:            string;
    profile?:        string;
    session_count?:  number;
    ssh_target:      string;
    status:          string;
    status_message?: string;
    [property: string]: any;
}

export enum EndpointStatusChangedMessageEvent {
    EndpointStatusChanged = "endpoint_status_changed",
}

export interface EndpointsUpdatedMessage {
    endpoints: Endpoint[];
    event:     EndpointsUpdatedMessageEvent;
    [property: string]: any;
}

export enum EndpointsUpdatedMessageEvent {
    EndpointsUpdated = "endpoints_updated",
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

export interface GetDispatchMessage {
    cmd:               GetDispatchMessageCmd;
    source_session_id: string;
    [property: string]: any;
}

export enum GetDispatchMessageCmd {
    GetDispatch = "get_dispatch",
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
    cmd:          GetRecentLocationsMessageCmd;
    endpoint_id?: string;
    limit?:       number;
    request_id?:  string;
    [property: string]: any;
}

export enum GetRecentLocationsMessageCmd {
    GetRecentLocations = "get_recent_locations",
}

export interface GetRepoInfoMessage {
    cmd:          GetRepoInfoMessageCmd;
    endpoint_id?: string;
    repo:         string;
    [property: string]: any;
}

export enum GetRepoInfoMessageCmd {
    GetRepoInfo = "get_repo_info",
}

export interface GetRepoInfoResultMessage {
    endpoint_id?: string;
    error?:       string;
    event:        GetRepoInfoResultMessageEvent;
    info?:        Info;
    success:      boolean;
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

export interface GetReviewLoopRunMessage {
    cmd:     GetReviewLoopRunMessageCmd;
    loop_id: string;
    [property: string]: any;
}

export enum GetReviewLoopRunMessageCmd {
    GetReviewLoopRun = "get_review_loop_run",
}

export interface GetReviewLoopStateMessage {
    cmd:        GetReviewLoopStateMessageCmd;
    session_id: string;
    [property: string]: any;
}

export enum GetReviewLoopStateMessageCmd {
    GetReviewLoopState = "get_review_loop_state",
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

export interface GetScreenSnapshotMessage {
    cmd: GetScreenSnapshotMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum GetScreenSnapshotMessageCmd {
    GetScreenSnapshot = "get_screen_snapshot",
}

export interface GetScreenSnapshotResultMessage {
    cols?:                  number;
    error?:                 string;
    event:                  GetScreenSnapshotResultMessageEvent;
    id:                     string;
    last_seq?:              number;
    rows?:                  number;
    running?:               boolean;
    screen_cols?:           number;
    screen_cursor_visible?: boolean;
    screen_cursor_x?:       number;
    screen_cursor_y?:       number;
    screen_rows?:           number;
    screen_snapshot?:       string;
    screen_snapshot_fresh?: boolean;
    success:                boolean;
    [property: string]: any;
}

export enum GetScreenSnapshotResultMessageEvent {
    GetScreenSnapshotResult = "get_screen_snapshot_result",
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

export interface GitHubHostsUpdatedMessage {
    event:        GitHubHostsUpdatedMessageEvent;
    github_hosts: string[];
    [property: string]: any;
}

export enum GitHubHostsUpdatedMessageEvent {
    GithubHostsUpdated = "github_hosts_updated",
}

export interface GitOperation {
    duration_ms?: number;
    endpoint_id?: string;
    error?:       string;
    finished_at?: string;
    id:           string;
    kind:         GitOperationKind;
    path?:        string;
    started_at:   string;
    status:       GitOperationStatus;
    [property: string]: any;
}

export enum GitOperationStatus {
    Failed = "failed",
    Running = "running",
    Succeeded = "succeeded",
}

export interface GitOperationFinishedMessage {
    event:     GitOperationFinishedMessageEvent;
    operation: Operation;
    [property: string]: any;
}

export enum GitOperationFinishedMessageEvent {
    GitOperationFinished = "git_operation_finished",
}

export interface Operation {
    duration_ms?: number;
    endpoint_id?: string;
    error?:       string;
    finished_at?: string;
    id:           string;
    kind:         GitOperationKind;
    path?:        string;
    started_at:   string;
    status:       GitOperationStatus;
    [property: string]: any;
}

export interface GitOperationStartedMessage {
    event:     GitOperationStartedMessageEvent;
    operation: Operation;
    [property: string]: any;
}

export enum GitOperationStartedMessageEvent {
    GitOperationStarted = "git_operation_started",
}

export interface GitStatusUpdateMessage {
    directory:       string;
    duration_ms?:    number;
    error?:          string;
    event:           GitStatusUpdateMessageEvent;
    limited?:        boolean;
    limited_reason?: string;
    mode?:           string;
    staged:          StagedElement[];
    unstaged:        StagedElement[];
    untracked:       StagedElement[];
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
    authors?:                   AuthorElement[];
    chief_of_staff_dispatches?: ChiefOfStaffDispatchElement[];
    daemon_instance_id?:        string;
    endpoints?:                 Endpoint[];
    event:                      InitialStateMessageEvent;
    github_hosts?:              string[];
    protocol_version?:          string;
    prs?:                       PRElement[];
    repos?:                     RepoElement[];
    sessions?:                  SessionElement[];
    settings?:                  { [key: string]: any };
    source_fingerprint?:        string;
    warnings?:                  WarningElement[];
    workspaces?:                WorkspaceElement[];
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

export interface WorkspaceElement {
    directory: string;
    id:        string;
    layout?:   Layout;
    muted:     boolean;
    rank:      string;
    status:    WorkspaceStatus;
    title:     string;
    [property: string]: any;
}

export interface Layout {
    active_pane_id: string;
    layout_json:    string;
    panes:          PaneElement[];
    updated_at?:    string;
    workspace_id:   string;
    [property: string]: any;
}

export interface PaneElement {
    error?:       string;
    kind:         WorkspaceLayoutPaneKind;
    pane_id:      string;
    runtime_id?:  string;
    session_id?:  string;
    status:       WorkspaceLayoutPaneStatus;
    title:        string;
    workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceLayoutPaneKind {
    Agent = "agent",
}

export enum WorkspaceLayoutPaneStatus {
    Failed = "failed",
    Ready = "ready",
    Spawning = "spawning",
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

export interface InspectPathMessage {
    cmd:          InspectPathMessageCmd;
    endpoint_id?: string;
    path:         string;
    request_id?:  string;
    [property: string]: any;
}

export enum InspectPathMessageCmd {
    InspectPath = "inspect_path",
}

export interface InspectPathResultMessage {
    endpoint_id?: string;
    error?:       string;
    event:        InspectPathResultMessageEvent;
    inspection?:  Inspection;
    request_id?:  string;
    success:      boolean;
    [property: string]: any;
}

export enum InspectPathResultMessageEvent {
    InspectPathResult = "inspect_path_result",
}

export interface Inspection {
    exists:        boolean;
    home_path?:    string;
    input_path:    string;
    is_directory:  boolean;
    repo_root?:    string;
    resolved_path: string;
    [property: string]: any;
}

export interface InstallPluginMessage {
    cmd:    InstallPluginMessageCmd;
    source: string;
    [property: string]: any;
}

export enum InstallPluginMessageCmd {
    InstallPlugin = "install_plugin",
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

export interface ListDispatchesMessage {
    cmd:               ListDispatchesMessageCmd;
    source_session_id: string;
    [property: string]: any;
}

export enum ListDispatchesMessageCmd {
    ListDispatches = "list_dispatches",
}

export interface ListDispatchMessagesMessage {
    cmd:               ListDispatchMessagesMessageCmd;
    dispatch_id?:      string;
    source_session_id: string;
    unread_only?:      boolean;
    [property: string]: any;
}

export enum ListDispatchMessagesMessageCmd {
    ListDispatchMessages = "list_dispatch_messages",
}

export interface ListEndpointsMessage {
    cmd: ListEndpointsMessageCmd;
    [property: string]: any;
}

export enum ListEndpointsMessageCmd {
    ListEndpoints = "list_endpoints",
}

export interface ListPluginsMessage {
    cmd: ListPluginsMessageCmd;
    [property: string]: any;
}

export enum ListPluginsMessageCmd {
    ListPlugins = "list_plugins",
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

export interface MuteWorkspaceMessage {
    cmd:          MuteWorkspaceMessageCmd;
    endpoint_id?: string;
    workspace_id: string;
    [property: string]: any;
}

export enum MuteWorkspaceMessageCmd {
    MuteWorkspace = "mute_workspace",
}

export interface OpenBrowserMessage {
    cmd:         OpenBrowserMessageCmd;
    session_id?: string;
    url:         string;
    [property: string]: any;
}

export enum OpenBrowserMessageCmd {
    OpenBrowser = "open_browser",
}

export interface OpenMarkdownMessage {
    cmd:         OpenMarkdownMessageCmd;
    path:        string;
    session_id?: string;
    [property: string]: any;
}

export enum OpenMarkdownMessageCmd {
    OpenMarkdown = "open_markdown",
}

export interface PathInspection {
    exists:        boolean;
    home_path?:    string;
    input_path:    string;
    is_directory:  boolean;
    repo_root?:    string;
    resolved_path: string;
    [property: string]: any;
}

export interface PluginActionResultMessage {
    action:  string;
    error?:  string;
    event:   PluginActionResultMessageEvent;
    name?:   string;
    success: boolean;
    [property: string]: any;
}

export enum PluginActionResultMessageEvent {
    PluginActionResult = "plugin_action_result",
}

export interface PluginInfo {
    connected:       boolean;
    description?:    string;
    dir:             string;
    health_message?: string;
    health_status?:  string;
    last_health_at?: string;
    name:            string;
    priority:        number;
    running:         boolean;
    version:         string;
    [property: string]: any;
}

export interface PluginIssue {
    error: string;
    path:  string;
    [property: string]: any;
}

export interface PluginsUpdatedMessage {
    event:   PluginsUpdatedMessageEvent;
    issues?: IssueElement[];
    plugins: PluginElement[];
    [property: string]: any;
}

export enum PluginsUpdatedMessageEvent {
    PluginsUpdated = "plugins_updated",
}

export interface IssueElement {
    error: string;
    path:  string;
    [property: string]: any;
}

export interface PluginElement {
    connected:       boolean;
    description?:    string;
    dir:             string;
    health_message?: string;
    health_status?:  string;
    last_health_at?: string;
    name:            string;
    priority:        number;
    running:         boolean;
    version:         string;
    [property: string]: any;
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

export interface PRsUpdatedMessage {
    event: PRsUpdatedMessageEvent;
    prs?:  PRElement[];
    [property: string]: any;
}

export enum PRsUpdatedMessageEvent {
    PrsUpdated = "prs_updated",
}

export interface PRVisitedMessage {
    cmd: PRVisitedMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum PRVisitedMessageCmd {
    PRVisited = "pr_visited",
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
    cmd:     PtyInputMessageCmd;
    data:    string;
    id:      string;
    source?: string;
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

export interface PtyResizedMessage {
    cols:  number;
    event: PtyResizedMessageEvent;
    id:    string;
    rows:  number;
    [property: string]: any;
}

export enum PtyResizedMessageEvent {
    PtyResized = "pty_resized",
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

export interface ReadDispatchMessage {
    cmd:               ReadDispatchMessageCmd;
    message_id:        string;
    source_session_id: string;
    [property: string]: any;
}

export enum ReadDispatchMessageCmd {
    ReadDispatchMessage = "read_dispatch_message",
}

export interface RecentLocation {
    last_seen: string;
    path:      string;
    use_count: number;
    [property: string]: any;
}

export interface RecentLocationsResultMessage {
    endpoint_id?:     string;
    error?:           string;
    event:            RecentLocationsResultMessageEvent;
    home_path?:       string;
    recent_locations: RecentLocationElement[];
    request_id?:      string;
    success:          boolean;
    [property: string]: any;
}

export enum RecentLocationsResultMessageEvent {
    RecentLocationsResult = "recent_locations_result",
}

export interface RecentLocationElement {
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
    agent?:       string;
    cmd:          RegisterMessageCmd;
    dir:          string;
    id:           string;
    label?:       string;
    workspace_id: string;
    [property: string]: any;
}

export enum RegisterMessageCmd {
    Register = "register",
}

export interface RegisterWorkspaceMessage {
    cmd:          RegisterWorkspaceMessageCmd;
    directory:    string;
    endpoint_id?: string;
    id:           string;
    title:        string;
    [property: string]: any;
}

export enum RegisterWorkspaceMessageCmd {
    RegisterWorkspace = "register_workspace",
}

export interface RemoveEndpointMessage {
    cmd:         RemoveEndpointMessageCmd;
    endpoint_id: string;
    [property: string]: any;
}

export enum RemoveEndpointMessageCmd {
    RemoveEndpoint = "remove_endpoint",
}

export interface RemovePluginMessage {
    cmd:  RemovePluginMessageCmd;
    name: string;
    [property: string]: any;
}

export enum RemovePluginMessageCmd {
    RemovePlugin = "remove_plugin",
}

export interface RenameResultMessage {
    cmd:     string;
    error?:  string;
    event:   RenameResultMessageEvent;
    id:      string;
    success: boolean;
    [property: string]: any;
}

export enum RenameResultMessageEvent {
    RenameResult = "rename_result",
}

export interface RenameSessionMessage {
    cmd:        RenameSessionMessageCmd;
    label:      string;
    session_id: string;
    [property: string]: any;
}

export enum RenameSessionMessageCmd {
    RenameSession = "rename_session",
}

export interface RenameWorkspaceMessage {
    cmd:          RenameWorkspaceMessageCmd;
    title:        string;
    workspace_id: string;
    [property: string]: any;
}

export enum RenameWorkspaceMessageCmd {
    RenameWorkspace = "rename_workspace",
}

export interface ReplaySegment {
    cols: number;
    data: string;
    rows: number;
    [property: string]: any;
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

export interface ReportDispatchMessage {
    cmd:                ReportDispatchMessageCmd;
    report:             string;
    source_session_id:  string;
    structured_report?: Report;
    [property: string]: any;
}

export enum ReportDispatchMessageCmd {
    ReportDispatch = "report_dispatch",
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

export interface ResolveDispatchRequestMessage {
    cmd:               ResolveDispatchRequestMessageCmd;
    dispatch_id:       string;
    resolution_link?:  string;
    response:          string;
    source_session_id: string;
    [property: string]: any;
}

export enum ResolveDispatchRequestMessageCmd {
    ResolveDispatchRequest = "resolve_dispatch_request",
}

export interface Response {
    authors?:                              AuthorElement[];
    chief_of_staff_dispatch?:              ChiefOfStaffDispatchElement;
    chief_of_staff_dispatches?:            ChiefOfStaffDispatchElement[];
    data?:                                 string;
    delegate_result?:                      DelegateResultObject;
    dispatch_message?:                     DispatchMessageObject;
    dispatch_messages?:                    DispatchMessageObject[];
    error?:                                string;
    ok:                                    boolean;
    prs?:                                  PRElement[];
    repos?:                                RepoElement[];
    review_loop_run?:                      ReviewLoopRunObject;
    sessions?:                             SessionElement[];
    workspace_context_maintenance_result?: WorkspaceContextMaintenanceResultObject;
    workspace_context_result?:             WorkspaceContextResultObject;
    workspace_contexts?:                   WorkspaceContextElement[];
    [property: string]: any;
}

export interface DispatchMessageObject {
    acknowledged_at?:  string;
    acknowledgement?:  string;
    content:           string;
    created_at:        string;
    dispatch_id:       string;
    id:                string;
    read_at?:          string;
    sender_session_id: string;
    target_session_id: string;
    [property: string]: any;
}

export interface ReviewLoopRunObject {
    completed_at?:           string;
    created_at:              string;
    custom_prompt?:          string;
    handoff_payload_json?:   string;
    iteration_count:         number;
    iteration_limit:         number;
    iterations?:             Iteration[];
    last_decision?:          ReviewLoopDecision;
    last_error?:             string;
    last_result_summary?:    string;
    latest_iteration?:       Iteration;
    loop_id:                 string;
    pending_interaction?:    Interaction;
    pending_interaction_id?: string;
    preset_id?:              string;
    repo_path:               string;
    resolved_prompt:         string;
    source_session_id:       string;
    status:                  ReviewLoopRunStatus;
    stop_reason?:            string;
    updated_at:              string;
    [property: string]: any;
}

export interface Iteration {
    assistant_trace_json?:   string;
    blocking_reason?:        string;
    change_stats?:           FileElement[];
    changes_made?:           boolean;
    completed_at?:           string;
    decision?:               ReviewLoopDecision;
    error?:                  string;
    files_touched?:          string[];
    id:                      string;
    iteration_number:        number;
    loop_id:                 string;
    result_text?:            string;
    started_at:              string;
    status:                  ReviewLoopIterationStatus;
    structured_output_json?: string;
    suggested_next_focus?:   string;
    summary?:                string;
    [property: string]: any;
}

export enum ReviewLoopDecision {
    Continue = "continue",
    Converged = "converged",
    Error = "error",
    NeedsUserInput = "needs_user_input",
}

export enum ReviewLoopIterationStatus {
    AwaitingUser = "awaiting_user",
    Cancelled = "cancelled",
    Completed = "completed",
    Error = "error",
    Running = "running",
}

export interface Interaction {
    answer?:       string;
    answered_at?:  string;
    consumed_at?:  string;
    created_at:    string;
    id:            string;
    iteration_id?: string;
    kind:          string;
    loop_id:       string;
    question:      string;
    status:        ReviewLoopInteractionStatus;
    [property: string]: any;
}

export enum ReviewLoopInteractionStatus {
    Answered = "answered",
    Consumed = "consumed",
    Pending = "pending",
}

export enum ReviewLoopRunStatus {
    AwaitingUser = "awaiting_user",
    Completed = "completed",
    Error = "error",
    Running = "running",
    Stopped = "stopped",
}

export interface WorkspaceContextMaintenanceResultObject {
    action:          WorkspaceContextMaintenanceAction;
    agent?:          string;
    agent_model?:    string;
    changed:         boolean;
    result_revision: number;
    source_revision: number;
    workspace_id:    string;
    [property: string]: any;
}

export enum WorkspaceContextMaintenanceAction {
    Compact = "compact",
    Rollback = "rollback",
}

export interface WorkspaceContextResultObject {
    canonical_revision:     number;
    modified:               boolean;
    path:                   string;
    revision:               number;
    session_id:             string;
    stale:                  boolean;
    updated_at?:            string;
    updated_by_session_id?: string;
    workspace_id:           string;
    [property: string]: any;
}

export interface WorkspaceContextElement {
    content:               string;
    revision:              number;
    updated_at:            string;
    updated_by_session_id: string;
    workspace_id:          string;
    [property: string]: any;
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
    [property: string]: any;
}

export interface ReviewLoopInteraction {
    answer?:       string;
    answered_at?:  string;
    consumed_at?:  string;
    created_at:    string;
    id:            string;
    iteration_id?: string;
    kind:          string;
    loop_id:       string;
    question:      string;
    status:        ReviewLoopInteractionStatus;
    [property: string]: any;
}

export interface ReviewLoopIteration {
    assistant_trace_json?:   string;
    blocking_reason?:        string;
    change_stats?:           FileElement[];
    changes_made?:           boolean;
    completed_at?:           string;
    decision?:               ReviewLoopDecision;
    error?:                  string;
    files_touched?:          string[];
    id:                      string;
    iteration_number:        number;
    loop_id:                 string;
    result_text?:            string;
    started_at:              string;
    status:                  ReviewLoopIterationStatus;
    structured_output_json?: string;
    suggested_next_focus?:   string;
    summary?:                string;
    [property: string]: any;
}

export interface ReviewLoopResultMessage {
    action:           string;
    error?:           string;
    event:            ReviewLoopResultMessageEvent;
    loop_id?:         string;
    review_loop_run?: ReviewLoopRunObject;
    session_id:       string;
    success:          boolean;
    [property: string]: any;
}

export enum ReviewLoopResultMessageEvent {
    ReviewLoopResult = "review_loop_result",
}

export interface ReviewLoopRun {
    completed_at?:           string;
    created_at:              string;
    custom_prompt?:          string;
    handoff_payload_json?:   string;
    iteration_count:         number;
    iteration_limit:         number;
    iterations?:             Iteration[];
    last_decision?:          ReviewLoopDecision;
    last_error?:             string;
    last_result_summary?:    string;
    latest_iteration?:       Iteration;
    loop_id:                 string;
    pending_interaction?:    Interaction;
    pending_interaction_id?: string;
    preset_id?:              string;
    repo_path:               string;
    resolved_prompt:         string;
    source_session_id:       string;
    status:                  ReviewLoopRunStatus;
    stop_reason?:            string;
    updated_at:              string;
    [property: string]: any;
}

export interface ReviewLoopState {
    advance_token:       string;
    created_at:          string;
    custom_prompt?:      string;
    iteration_count:     number;
    iteration_limit:     number;
    last_advance_at?:    string;
    last_prompt_at?:     string;
    last_user_input_at?: string;
    preset_id?:          string;
    resolved_prompt:     string;
    session_id:          string;
    status:              ReviewLoopStatus;
    stop_reason?:        string;
    stop_requested:      boolean;
    updated_at:          string;
    [property: string]: any;
}

export enum ReviewLoopStatus {
    AdvanceReceivedWaitingPrompt = "advance_received_waiting_prompt",
    Completed = "completed",
    Error = "error",
    Running = "running",
    Stopped = "stopped",
    WaitingForAgentAdvance = "waiting_for_agent_advance",
}

export interface ReviewLoopUpdatedMessage {
    event:            ReviewLoopUpdatedMessageEvent;
    review_loop_run?: ReviewLoopRunObject;
    session_id:       string;
    [property: string]: any;
}

export enum ReviewLoopUpdatedMessageEvent {
    ReviewLoopUpdated = "review_loop_updated",
}

export interface ReviewState {
    branch:       string;
    repo_path:    string;
    review_id:    string;
    viewed_files: string[];
    [property: string]: any;
}

export interface SendDispatchMessage {
    cmd:               SendDispatchMessageCmd;
    content:           string;
    dispatch_id:       string;
    source_session_id: string;
    [property: string]: any;
}

export enum SendDispatchMessageCmd {
    SendDispatchMessage = "send_dispatch_message",
}

export interface Session {
    agent:                        string;
    branch?:                      string;
    chief_of_staff?:              boolean;
    directory:                    string;
    endpoint_id?:                 string;
    id:                           string;
    is_worktree?:                 boolean;
    label:                        string;
    last_seen:                    string;
    main_repo?:                   string;
    needs_review_after_long_run?: boolean;
    recoverable?:                 boolean;
    state:                        WorkspaceStatus;
    state_since:                  string;
    state_updated_at:             string;
    todos?:                       string[];
    workspace_id:                 string;
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

export interface SessionSelectedMessage {
    cmd: SessionSelectedMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum SessionSelectedMessageCmd {
    SessionSelected = "session_selected",
}

export interface SessionStateChangedMessage {
    event:   SessionStateChangedMessageEvent;
    session: SessionElement;
    [property: string]: any;
}

export enum SessionStateChangedMessageEvent {
    SessionStateChanged = "session_state_changed",
}

export interface SessionsUpdatedMessage {
    event:     SessionsUpdatedMessageEvent;
    sessions?: SessionElement[];
    [property: string]: any;
}

export enum SessionsUpdatedMessageEvent {
    SessionsUpdated = "sessions_updated",
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

export interface SetChiefOfStaffMessage {
    chief_of_staff: boolean;
    cmd:            SetChiefOfStaffMessageCmd;
    session_id:     string;
    [property: string]: any;
}

export enum SetChiefOfStaffMessageCmd {
    SetChiefOfStaff = "set_chief_of_staff",
}

export interface SetEndpointRemoteWebMessage {
    cmd:         SetEndpointRemoteWebMessageCmd;
    enabled:     boolean;
    endpoint_id: string;
    [property: string]: any;
}

export enum SetEndpointRemoteWebMessageCmd {
    SetEndpointRemoteWeb = "set_endpoint_remote_web",
}

export interface SetPluginPriorityMessage {
    cmd:      SetPluginPriorityMessageCmd;
    name:     string;
    priority: number;
    [property: string]: any;
}

export enum SetPluginPriorityMessageCmd {
    SetPluginPriority = "set_plugin_priority",
}

export interface SetReviewLoopIterationLimitMessage {
    cmd:             SetReviewLoopIterationLimitMessageCmd;
    iteration_limit: number;
    session_id:      string;
    [property: string]: any;
}

export enum SetReviewLoopIterationLimitMessageCmd {
    SetReviewLoopIterationLimit = "set_review_loop_iteration_limit",
}

export interface SetSessionResumeIDMessage {
    cmd:               SetSessionResumeIDMessageCmd;
    id:                string;
    resume_session_id: string;
    [property: string]: any;
}

export enum SetSessionResumeIDMessageCmd {
    SetSessionResumeID = "set_session_resume_id",
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
    changed_key?: string;
    error?:       string;
    event:        SettingsUpdatedMessageEvent;
    settings?:    { [key: string]: any };
    success?:     boolean;
    [property: string]: any;
}

export enum SettingsUpdatedMessageEvent {
    SettingsUpdated = "settings_updated",
}

export interface SetWorkspaceRankMessage {
    cmd:                SetWorkspaceRankMessageCmd;
    next_workspace_id?: string;
    prev_workspace_id?: string;
    workspace_id:       string;
    [property: string]: any;
}

export enum SetWorkspaceRankMessageCmd {
    SetWorkspaceRank = "set_workspace_rank",
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
    endpoint_id?:        string;
    executable?:         string;
    id:                  string;
    initial_prompt?:     string;
    label?:              string;
    resume_picker?:      boolean;
    resume_session_id?:  string;
    rows:                number;
    workspace_id:        string;
    yolo_mode?:          boolean;
    [property: string]: any;
}

export enum SpawnSessionMessageCmd {
    SpawnSession = "spawn_session",
}

export interface StartReviewLoopMessage {
    cmd:                   StartReviewLoopMessageCmd;
    handoff_payload_json?: string;
    iteration_limit:       number;
    preset_id?:            string;
    prompt:                string;
    session_id:            string;
    [property: string]: any;
}

export enum StartReviewLoopMessageCmd {
    StartReviewLoop = "start_review_loop",
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

export interface StopReviewLoopMessage {
    cmd:        StopReviewLoopMessageCmd;
    session_id: string;
    [property: string]: any;
}

export enum StopReviewLoopMessageCmd {
    StopReviewLoop = "stop_review_loop",
}

export interface SubscribeGitStatusMessage {
    cmd:       SubscribeGitStatusMessageCmd;
    directory: string;
    [property: string]: any;
}

export enum SubscribeGitStatusMessageCmd {
    SubscribeGitStatus = "subscribe_git_status",
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

export interface UnregisterWorkspaceMessage {
    cmd: UnregisterWorkspaceMessageCmd;
    id:  string;
    [property: string]: any;
}

export enum UnregisterWorkspaceMessageCmd {
    UnregisterWorkspace = "unregister_workspace",
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

export interface UpdateEndpointMessage {
    cmd:         UpdateEndpointMessageCmd;
    enabled?:    boolean;
    endpoint_id: string;
    name?:       string;
    profile?:    string;
    ssh_target?: string;
    [property: string]: any;
}

export enum UpdateEndpointMessageCmd {
    UpdateEndpoint = "update_endpoint",
}

export interface WakeDispatchAgentMessage {
    cmd:               WakeDispatchAgentMessageCmd;
    dispatch_id:       string;
    request_id:        string;
    source_session_id: string;
    [property: string]: any;
}

export enum WakeDispatchAgentMessageCmd {
    WakeDispatchAgent = "wake_dispatch_agent",
}

export interface WakeDispatchAgentResultMessage {
    dispatch_id: string;
    error?:      string;
    event:       WakeDispatchAgentResultMessageEvent;
    request_id:  string;
    success:     boolean;
    [property: string]: any;
}

export enum WakeDispatchAgentResultMessageEvent {
    WakeDispatchAgentResult = "wake_dispatch_agent_result",
}

export interface WebSocketEvent {
    action?:                    string;
    authors?:                   AuthorElement[];
    base_ref?:                  string;
    branch?:                    string;
    branches?:                  BranchElement[];
    chief_of_staff?:            boolean;
    chief_of_staff_dispatch?:   ChiefOfStaffDispatchElement;
    chief_of_staff_dispatches?: ChiefOfStaffDispatchElement[];
    cloned?:                    boolean;
    cmd?:                       string;
    cols?:                      number;
    conflict?:                  boolean;
    content?:                   string;
    data?:                      string;
    directory?:                 string;
    dirty?:                     boolean;
    dispatch_message?:          DispatchMessageObject;
    dispatch_messages?:         DispatchMessageObject[];
    error?:                     string;
    event:                      string;
    exit_code?:                 number;
    files?:                     FileElement[];
    found?:                     boolean;
    id?:                        string;
    last_seq?:                  number;
    modified?:                  string;
    name?:                      string;
    operation?:                 Operation;
    original?:                  string;
    pane_id?:                   string;
    path?:                      string;
    pid?:                       number;
    plugin_issues?:             IssueElement[];
    plugins?:                   PluginElement[];
    previous_session_id?:       string;
    priority?:                  number;
    protocol_version?:          string;
    prs?:                       PRElement[];
    rate_limit_reset_at?:       string;
    rate_limit_resource?:       string;
    reason?:                    string;
    recent_locations?:          RecentLocationElement[];
    repos?:                     RepoElement[];
    review_loop_run?:           ReviewLoopRunObject;
    rows?:                      number;
    running?:                   boolean;
    runtime_id?:                string;
    screen_cols?:               number;
    screen_cursor_visible?:     boolean;
    screen_cursor_x?:           number;
    screen_cursor_y?:           number;
    screen_rows?:               number;
    screen_snapshot?:           string;
    screen_snapshot_fresh?:     boolean;
    scrollback?:                string;
    scrollback_truncated?:      boolean;
    seq?:                       number;
    session?:                   SessionElement;
    session_id?:                string;
    sessions?:                  SessionElement[];
    settings?:                  { [key: string]: any };
    signal?:                    string;
    split_id?:                  string;
    staged?:                    StagedElement[];
    stash_ref?:                 string;
    success?:                   boolean;
    target_path?:               string;
    tile_id?:                   string;
    tile_kind?:                 string;
    unstaged?:                  StagedElement[];
    untracked?:                 StagedElement[];
    warnings?:                  WarningElement[];
    workspace?:                 WorkspaceElement;
    workspace_context_result?:  WorkspaceContextResultObject;
    workspace_contexts?:        WorkspaceContextElement[];
    workspace_id?:              string;
    workspace_layout?:          Layout;
    workspaces?:                WorkspaceElement[];
    worktrees?:                 WorktreeElement[];
    [property: string]: any;
}

export interface Workspace {
    directory: string;
    id:        string;
    layout?:   Layout;
    muted:     boolean;
    rank:      string;
    status:    WorkspaceStatus;
    title:     string;
    [property: string]: any;
}

export interface WorkspaceContext {
    content:               string;
    revision:              number;
    updated_at:            string;
    updated_by_session_id: string;
    workspace_id:          string;
    [property: string]: any;
}

export interface WorkspaceContextChangedMessage {
    event:                 WorkspaceContextChangedMessageEvent;
    revision:              number;
    updated_at:            string;
    updated_by_session_id: string;
    workspace_id:          string;
    [property: string]: any;
}

export enum WorkspaceContextChangedMessageEvent {
    WorkspaceContextChanged = "workspace_context_changed",
}

export interface WorkspaceContextCheckoutMessage {
    cmd:               WorkspaceContextCheckoutMessageCmd;
    force?:            boolean;
    source_session_id: string;
    [property: string]: any;
}

export enum WorkspaceContextCheckoutMessageCmd {
    WorkspaceContextCheckout = "workspace_context_checkout",
}

export interface WorkspaceContextCompactMessage {
    cmd:               WorkspaceContextCompactMessageCmd;
    source_session_id: string;
    [property: string]: any;
}

export enum WorkspaceContextCompactMessageCmd {
    WorkspaceContextCompact = "workspace_context_compact",
}

export interface WorkspaceContextListMessage {
    cmd:        WorkspaceContextListMessageCmd;
    request_id: string;
    [property: string]: any;
}

export enum WorkspaceContextListMessageCmd {
    WorkspaceContextList = "workspace_context_list",
}

export interface WorkspaceContextListResultMessage {
    contexts?:  WorkspaceContextElement[];
    error?:     string;
    event:      WorkspaceContextListResultMessageEvent;
    request_id: string;
    success:    boolean;
    [property: string]: any;
}

export enum WorkspaceContextListResultMessageEvent {
    WorkspaceContextListResult = "workspace_context_list_result",
}

export interface WorkspaceContextMaintenanceResult {
    action:          WorkspaceContextMaintenanceAction;
    agent?:          string;
    agent_model?:    string;
    changed:         boolean;
    result_revision: number;
    source_revision: number;
    workspace_id:    string;
    [property: string]: any;
}

export interface WorkspaceContextResult {
    canonical_revision:     number;
    modified:               boolean;
    path:                   string;
    revision:               number;
    session_id:             string;
    stale:                  boolean;
    updated_at?:            string;
    updated_by_session_id?: string;
    workspace_id:           string;
    [property: string]: any;
}

export interface WorkspaceContextResultMessage {
    action:  string;
    error?:  string;
    event:   WorkspaceContextResultMessageEvent;
    result?: WorkspaceContextResultObject;
    success: boolean;
    [property: string]: any;
}

export enum WorkspaceContextResultMessageEvent {
    WorkspaceContextResult = "workspace_context_result",
}

export interface WorkspaceContextRollbackMessage {
    cmd:               WorkspaceContextRollbackMessageCmd;
    source_session_id: string;
    [property: string]: any;
}

export enum WorkspaceContextRollbackMessageCmd {
    WorkspaceContextRollback = "workspace_context_rollback",
}

export interface WorkspaceContextStatusMessage {
    cmd:               WorkspaceContextStatusMessageCmd;
    source_session_id: string;
    [property: string]: any;
}

export enum WorkspaceContextStatusMessageCmd {
    WorkspaceContextStatus = "workspace_context_status",
}

export interface WorkspaceContextUpdateMessage {
    cmd:               WorkspaceContextUpdateMessageCmd;
    source_session_id: string;
    [property: string]: any;
}

export enum WorkspaceContextUpdateMessageCmd {
    WorkspaceContextUpdate = "workspace_context_update",
}

export interface WorkspaceLayout {
    active_pane_id: string;
    layout_json:    string;
    panes:          PaneElement[];
    updated_at?:    string;
    workspace_id:   string;
    [property: string]: any;
}

export interface WorkspaceLayoutActionResultMessage {
    action:               string;
    error?:               string;
    event:                WorkspaceLayoutActionResultMessageEvent;
    final_leaf_id?:       string;
    leaf_id?:             string;
    pane_id?:             string;
    request_id?:          string;
    source_workspace_id?: string;
    split_id?:            string;
    success:              boolean;
    target_workspace_id?: string;
    tile_id?:             string;
    workspace_id:         string;
    [property: string]: any;
}

export enum WorkspaceLayoutActionResultMessageEvent {
    WorkspaceLayoutActionResult = "workspace_layout_action_result",
}

export interface WorkspaceLayoutAddSessionPaneMessage {
    cmd:             WorkspaceLayoutAddSessionPaneMessageCmd;
    direction?:      WorkspaceLayoutSplitDirection;
    pane_id?:        string;
    session_id:      string;
    target_pane_id?: string;
    title?:          string;
    workspace_id:    string;
    [property: string]: any;
}

export enum WorkspaceLayoutAddSessionPaneMessageCmd {
    WorkspaceLayoutAddSessionPane = "workspace_layout_add_session_pane",
}

export enum WorkspaceLayoutSplitDirection {
    Horizontal = "horizontal",
    Vertical = "vertical",
}

export interface WorkspaceLayoutClosePaneMessage {
    cmd:          WorkspaceLayoutClosePaneMessageCmd;
    pane_id:      string;
    workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceLayoutClosePaneMessageCmd {
    WorkspaceLayoutClosePane = "workspace_layout_close_pane",
}

export interface WorkspaceLayoutDockTileMessage {
    anchor_pane_id: string;
    cmd:            WorkspaceLayoutDockTileMessageCmd;
    edge:           WorkspaceLayoutDockEdge;
    ratio?:         number;
    tile_id:        string;
    tile_kind:      string;
    workspace_id:   string;
    [property: string]: any;
}

export enum WorkspaceLayoutDockTileMessageCmd {
    WorkspaceLayoutDockTile = "workspace_layout_dock_tile",
}

export enum WorkspaceLayoutDockEdge {
    Bottom = "bottom",
    Left = "left",
    Right = "right",
    Top = "top",
}

export interface WorkspaceLayoutFocusPaneMessage {
    cmd:          WorkspaceLayoutFocusPaneMessageCmd;
    pane_id:      string;
    workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceLayoutFocusPaneMessageCmd {
    WorkspaceLayoutFocusPane = "workspace_layout_focus_pane",
}

export interface WorkspaceLayoutGetMessage {
    cmd:          WorkspaceLayoutGetMessageCmd;
    workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceLayoutGetMessageCmd {
    WorkspaceLayoutGet = "workspace_layout_get",
}

export interface WorkspaceLayoutMessage {
    event:            WorkspaceLayoutMessageEvent;
    workspace_layout: Layout;
    [property: string]: any;
}

export enum WorkspaceLayoutMessageEvent {
    WorkspaceLayout = "workspace_layout",
}

export interface WorkspaceLayoutMoveLeafMessage {
    anchor_id:    string;
    cmd:          WorkspaceLayoutMoveLeafMessageCmd;
    edge:         WorkspaceLayoutDockEdge;
    leaf_id:      string;
    ratio?:       number;
    workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceLayoutMoveLeafMessageCmd {
    WorkspaceLayoutMoveLeaf = "workspace_layout_move_leaf",
}

export interface WorkspaceLayoutMoveLeafToNewWorkspaceMessage {
    anchor_id?:          string;
    cmd:                 WorkspaceLayoutMoveLeafToNewWorkspaceMessageCmd;
    edge?:               WorkspaceLayoutDockEdge;
    leaf_id:             string;
    ratio?:              number;
    source_workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceLayoutMoveLeafToNewWorkspaceMessageCmd {
    WorkspaceLayoutMoveLeafToNewWorkspace = "workspace_layout_move_leaf_to_new_workspace",
}

export interface WorkspaceLayoutMoveLeafToWorkspaceMessage {
    anchor_id?:          string;
    cmd:                 WorkspaceLayoutMoveLeafToWorkspaceMessageCmd;
    edge:                WorkspaceLayoutDockEdge;
    leaf_id:             string;
    ratio?:              number;
    source_workspace_id: string;
    target_workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceLayoutMoveLeafToWorkspaceMessageCmd {
    WorkspaceLayoutMoveLeafToWorkspace = "workspace_layout_move_leaf_to_workspace",
}

export interface WorkspaceLayoutPane {
    error?:       string;
    kind:         WorkspaceLayoutPaneKind;
    pane_id:      string;
    runtime_id?:  string;
    session_id?:  string;
    status:       WorkspaceLayoutPaneStatus;
    title:        string;
    workspace_id: string;
    [property: string]: any;
}

export interface WorkspaceLayoutRenamePaneMessage {
    cmd:          WorkspaceLayoutRenamePaneMessageCmd;
    pane_id:      string;
    title:        string;
    workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceLayoutRenamePaneMessageCmd {
    WorkspaceLayoutRenamePane = "workspace_layout_rename_pane",
}

export interface WorkspaceLayoutSetSplitRatioMessage {
    cmd:          WorkspaceLayoutSetSplitRatioMessageCmd;
    ratio:        number;
    request_id?:  string;
    split_id:     string;
    workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceLayoutSetSplitRatioMessageCmd {
    WorkspaceLayoutSetSplitRatio = "workspace_layout_set_split_ratio",
}

export interface WorkspaceLayoutUndockTileMessage {
    cmd:          WorkspaceLayoutUndockTileMessageCmd;
    tile_id:      string;
    workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceLayoutUndockTileMessageCmd {
    WorkspaceLayoutUndockTile = "workspace_layout_undock_tile",
}

export interface WorkspaceLayoutUpdatedMessage {
    event:            WorkspaceLayoutUpdatedMessageEvent;
    workspace_layout: Layout;
    [property: string]: any;
}

export enum WorkspaceLayoutUpdatedMessageEvent {
    WorkspaceLayoutUpdated = "workspace_layout_updated",
}

export interface WorkspaceLayoutUpdateTileMessage {
    cmd:          WorkspaceLayoutUpdateTileMessageCmd;
    request_id:   string;
    tile_id:      string;
    tile_params:  string;
    workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceLayoutUpdateTileMessageCmd {
    WorkspaceLayoutUpdateTile = "workspace_layout_update_tile",
}

export interface WorkspaceRegisteredMessage {
    event:     WorkspaceRegisteredMessageEvent;
    workspace: WorkspaceElement;
    [property: string]: any;
}

export enum WorkspaceRegisteredMessageEvent {
    WorkspaceRegistered = "workspace_registered",
}

export interface WorkspaceSelectedMessage {
    cmd:          WorkspaceSelectedMessageCmd;
    workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceSelectedMessageCmd {
    WorkspaceSelected = "workspace_selected",
}

export interface WorkspaceStateChangedMessage {
    event:     WorkspaceStateChangedMessageEvent;
    workspace: WorkspaceElement;
    [property: string]: any;
}

export enum WorkspaceStateChangedMessageEvent {
    WorkspaceStateChanged = "workspace_state_changed",
}

export interface WorkspaceTileContentGetMessage {
    cmd:          WorkspaceTileContentGetMessageCmd;
    tile_id:      string;
    workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceTileContentGetMessageCmd {
    WorkspaceTileContentGet = "workspace_tile_content_get",
}

export interface WorkspaceTileContentMessage {
    content:      string;
    error?:       string;
    event:        WorkspaceTileContentMessageEvent;
    path:         string;
    tile_id:      string;
    tile_kind:    string;
    workspace_id: string;
    [property: string]: any;
}

export enum WorkspaceTileContentMessageEvent {
    WorkspaceTileContent = "workspace_tile_content",
}

export interface WorkspaceUnregisteredMessage {
    event:     WorkspaceUnregisteredMessageEvent;
    workspace: WorkspaceElement;
    [property: string]: any;
}

export enum WorkspaceUnregisteredMessageEvent {
    WorkspaceUnregistered = "workspace_unregistered",
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
    public static toAcknowledgeDispatchMessage(json: string): AcknowledgeDispatchMessage {
        return cast(JSON.parse(json), r("AcknowledgeDispatchMessage"));
    }

    public static acknowledgeDispatchMessageToJson(value: AcknowledgeDispatchMessage): string {
        return JSON.stringify(uncast(value, r("AcknowledgeDispatchMessage")), null, 2);
    }

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

    public static toAddEndpointMessage(json: string): AddEndpointMessage {
        return cast(JSON.parse(json), r("AddEndpointMessage"));
    }

    public static addEndpointMessageToJson(value: AddEndpointMessage): string {
        return JSON.stringify(uncast(value, r("AddEndpointMessage")), null, 2);
    }

    public static toAnswerReviewLoopMessage(json: string): AnswerReviewLoopMessage {
        return cast(JSON.parse(json), r("AnswerReviewLoopMessage"));
    }

    public static answerReviewLoopMessageToJson(value: AnswerReviewLoopMessage): string {
        return JSON.stringify(uncast(value, r("AnswerReviewLoopMessage")), null, 2);
    }

    public static toApprovePRMessage(json: string): ApprovePRMessage {
        return cast(JSON.parse(json), r("ApprovePRMessage"));
    }

    public static approvePRMessageToJson(value: ApprovePRMessage): string {
        return JSON.stringify(uncast(value, r("ApprovePRMessage")), null, 2);
    }

    public static toAttachPolicy(json: string): AttachPolicy {
        return cast(JSON.parse(json), r("AttachPolicy"));
    }

    public static attachPolicyToJson(value: AttachPolicy): string {
        return JSON.stringify(uncast(value, r("AttachPolicy")), null, 2);
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

    public static toBootstrapEndpointMessage(json: string): BootstrapEndpointMessage {
        return cast(JSON.parse(json), r("BootstrapEndpointMessage"));
    }

    public static bootstrapEndpointMessageToJson(value: BootstrapEndpointMessage): string {
        return JSON.stringify(uncast(value, r("BootstrapEndpointMessage")), null, 2);
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

    public static toBrowseDirectoryMessage(json: string): BrowseDirectoryMessage {
        return cast(JSON.parse(json), r("BrowseDirectoryMessage"));
    }

    public static browseDirectoryMessageToJson(value: BrowseDirectoryMessage): string {
        return JSON.stringify(uncast(value, r("BrowseDirectoryMessage")), null, 2);
    }

    public static toBrowseDirectoryResultMessage(json: string): BrowseDirectoryResultMessage {
        return cast(JSON.parse(json), r("BrowseDirectoryResultMessage"));
    }

    public static browseDirectoryResultMessageToJson(value: BrowseDirectoryResultMessage): string {
        return JSON.stringify(uncast(value, r("BrowseDirectoryResultMessage")), null, 2);
    }

    public static toBrowserControlMessage(json: string): BrowserControlMessage {
        return cast(JSON.parse(json), r("BrowserControlMessage"));
    }

    public static browserControlMessageToJson(value: BrowserControlMessage): string {
        return JSON.stringify(uncast(value, r("BrowserControlMessage")), null, 2);
    }

    public static toBrowserControlRequestMessage(json: string): BrowserControlRequestMessage {
        return cast(JSON.parse(json), r("BrowserControlRequestMessage"));
    }

    public static browserControlRequestMessageToJson(value: BrowserControlRequestMessage): string {
        return JSON.stringify(uncast(value, r("BrowserControlRequestMessage")), null, 2);
    }

    public static toBrowserControlResponseMessage(json: string): BrowserControlResponseMessage {
        return cast(JSON.parse(json), r("BrowserControlResponseMessage"));
    }

    public static browserControlResponseMessageToJson(value: BrowserControlResponseMessage): string {
        return JSON.stringify(uncast(value, r("BrowserControlResponseMessage")), null, 2);
    }

    public static toBrowserControlResultMessage(json: string): BrowserControlResultMessage {
        return cast(JSON.parse(json), r("BrowserControlResultMessage"));
    }

    public static browserControlResultMessageToJson(value: BrowserControlResultMessage): string {
        return JSON.stringify(uncast(value, r("BrowserControlResultMessage")), null, 2);
    }

    public static toChiefOfStaffDispatch(json: string): ChiefOfStaffDispatch {
        return cast(JSON.parse(json), r("ChiefOfStaffDispatch"));
    }

    public static chiefOfStaffDispatchToJson(value: ChiefOfStaffDispatch): string {
        return JSON.stringify(uncast(value, r("ChiefOfStaffDispatch")), null, 2);
    }

    public static toChiefOfStaffDispatchesUpdatedMessage(json: string): ChiefOfStaffDispatchesUpdatedMessage {
        return cast(JSON.parse(json), r("ChiefOfStaffDispatchesUpdatedMessage"));
    }

    public static chiefOfStaffDispatchesUpdatedMessageToJson(value: ChiefOfStaffDispatchesUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("ChiefOfStaffDispatchesUpdatedMessage")), null, 2);
    }

    public static toChiefOfStaffResultMessage(json: string): ChiefOfStaffResultMessage {
        return cast(JSON.parse(json), r("ChiefOfStaffResultMessage"));
    }

    public static chiefOfStaffResultMessageToJson(value: ChiefOfStaffResultMessage): string {
        return JSON.stringify(uncast(value, r("ChiefOfStaffResultMessage")), null, 2);
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

    public static toClientHelloMessage(json: string): ClientHelloMessage {
        return cast(JSON.parse(json), r("ClientHelloMessage"));
    }

    public static clientHelloMessageToJson(value: ClientHelloMessage): string {
        return JSON.stringify(uncast(value, r("ClientHelloMessage")), null, 2);
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

    public static toDelegateMessage(json: string): DelegateMessage {
        return cast(JSON.parse(json), r("DelegateMessage"));
    }

    public static delegateMessageToJson(value: DelegateMessage): string {
        return JSON.stringify(uncast(value, r("DelegateMessage")), null, 2);
    }

    public static toDelegateResult(json: string): DelegateResult {
        return cast(JSON.parse(json), r("DelegateResult"));
    }

    public static delegateResultToJson(value: DelegateResult): string {
        return JSON.stringify(uncast(value, r("DelegateResult")), null, 2);
    }

    public static toDelegateResultMessage(json: string): DelegateResultMessage {
        return cast(JSON.parse(json), r("DelegateResultMessage"));
    }

    public static delegateResultMessageToJson(value: DelegateResultMessage): string {
        return JSON.stringify(uncast(value, r("DelegateResultMessage")), null, 2);
    }

    public static toDelegateWorktreeRequest(json: string): DelegateWorktreeRequest {
        return cast(JSON.parse(json), r("DelegateWorktreeRequest"));
    }

    public static delegateWorktreeRequestToJson(value: DelegateWorktreeRequest): string {
        return JSON.stringify(uncast(value, r("DelegateWorktreeRequest")), null, 2);
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

    public static toDirectoryEntry(json: string): DirectoryEntry {
        return cast(JSON.parse(json), r("DirectoryEntry"));
    }

    public static directoryEntryToJson(value: DirectoryEntry): string {
        return JSON.stringify(uncast(value, r("DirectoryEntry")), null, 2);
    }

    public static toDispatchArtifact(json: string): DispatchArtifact {
        return cast(JSON.parse(json), r("DispatchArtifact"));
    }

    public static dispatchArtifactToJson(value: DispatchArtifact): string {
        return JSON.stringify(uncast(value, r("DispatchArtifact")), null, 2);
    }

    public static toDispatchDecisionRequest(json: string): DispatchDecisionRequest {
        return cast(JSON.parse(json), r("DispatchDecisionRequest"));
    }

    public static dispatchDecisionRequestToJson(value: DispatchDecisionRequest): string {
        return JSON.stringify(uncast(value, r("DispatchDecisionRequest")), null, 2);
    }

    public static toDispatchMessage(json: string): DispatchMessage {
        return cast(JSON.parse(json), r("DispatchMessage"));
    }

    public static dispatchMessageToJson(value: DispatchMessage): string {
        return JSON.stringify(uncast(value, r("DispatchMessage")), null, 2);
    }

    public static toDispatchReport(json: string): DispatchReport {
        return cast(JSON.parse(json), r("DispatchReport"));
    }

    public static dispatchReportToJson(value: DispatchReport): string {
        return JSON.stringify(uncast(value, r("DispatchReport")), null, 2);
    }

    public static toDispatchReportType(json: string): DispatchReportType {
        return cast(JSON.parse(json), r("DispatchReportType"));
    }

    public static dispatchReportTypeToJson(value: DispatchReportType): string {
        return JSON.stringify(uncast(value, r("DispatchReportType")), null, 2);
    }

    public static toDispatchRequestStatus(json: string): DispatchRequestStatus {
        return cast(JSON.parse(json), r("DispatchRequestStatus"));
    }

    public static dispatchRequestStatusToJson(value: DispatchRequestStatus): string {
        return JSON.stringify(uncast(value, r("DispatchRequestStatus")), null, 2);
    }

    public static toDispatchVerification(json: string): DispatchVerification {
        return cast(JSON.parse(json), r("DispatchVerification"));
    }

    public static dispatchVerificationToJson(value: DispatchVerification): string {
        return JSON.stringify(uncast(value, r("DispatchVerification")), null, 2);
    }

    public static toDispatchWorkState(json: string): DispatchWorkState {
        return cast(JSON.parse(json), r("DispatchWorkState"));
    }

    public static dispatchWorkStateToJson(value: DispatchWorkState): string {
        return JSON.stringify(uncast(value, r("DispatchWorkState")), null, 2);
    }

    public static toEndpointActionResultMessage(json: string): EndpointActionResultMessage {
        return cast(JSON.parse(json), r("EndpointActionResultMessage"));
    }

    public static endpointActionResultMessageToJson(value: EndpointActionResultMessage): string {
        return JSON.stringify(uncast(value, r("EndpointActionResultMessage")), null, 2);
    }

    public static toEndpointCapabilities(json: string): EndpointCapabilities {
        return cast(JSON.parse(json), r("EndpointCapabilities"));
    }

    public static endpointCapabilitiesToJson(value: EndpointCapabilities): string {
        return JSON.stringify(uncast(value, r("EndpointCapabilities")), null, 2);
    }

    public static toEndpointInfo(json: string): EndpointInfo {
        return cast(JSON.parse(json), r("EndpointInfo"));
    }

    public static endpointInfoToJson(value: EndpointInfo): string {
        return JSON.stringify(uncast(value, r("EndpointInfo")), null, 2);
    }

    public static toEndpointStatusChangedMessage(json: string): EndpointStatusChangedMessage {
        return cast(JSON.parse(json), r("EndpointStatusChangedMessage"));
    }

    public static endpointStatusChangedMessageToJson(value: EndpointStatusChangedMessage): string {
        return JSON.stringify(uncast(value, r("EndpointStatusChangedMessage")), null, 2);
    }

    public static toEndpointsUpdatedMessage(json: string): EndpointsUpdatedMessage {
        return cast(JSON.parse(json), r("EndpointsUpdatedMessage"));
    }

    public static endpointsUpdatedMessageToJson(value: EndpointsUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("EndpointsUpdatedMessage")), null, 2);
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

    public static toGetDispatchMessage(json: string): GetDispatchMessage {
        return cast(JSON.parse(json), r("GetDispatchMessage"));
    }

    public static getDispatchMessageToJson(value: GetDispatchMessage): string {
        return JSON.stringify(uncast(value, r("GetDispatchMessage")), null, 2);
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

    public static toGetReviewLoopRunMessage(json: string): GetReviewLoopRunMessage {
        return cast(JSON.parse(json), r("GetReviewLoopRunMessage"));
    }

    public static getReviewLoopRunMessageToJson(value: GetReviewLoopRunMessage): string {
        return JSON.stringify(uncast(value, r("GetReviewLoopRunMessage")), null, 2);
    }

    public static toGetReviewLoopStateMessage(json: string): GetReviewLoopStateMessage {
        return cast(JSON.parse(json), r("GetReviewLoopStateMessage"));
    }

    public static getReviewLoopStateMessageToJson(value: GetReviewLoopStateMessage): string {
        return JSON.stringify(uncast(value, r("GetReviewLoopStateMessage")), null, 2);
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

    public static toGetScreenSnapshotMessage(json: string): GetScreenSnapshotMessage {
        return cast(JSON.parse(json), r("GetScreenSnapshotMessage"));
    }

    public static getScreenSnapshotMessageToJson(value: GetScreenSnapshotMessage): string {
        return JSON.stringify(uncast(value, r("GetScreenSnapshotMessage")), null, 2);
    }

    public static toGetScreenSnapshotResultMessage(json: string): GetScreenSnapshotResultMessage {
        return cast(JSON.parse(json), r("GetScreenSnapshotResultMessage"));
    }

    public static getScreenSnapshotResultMessageToJson(value: GetScreenSnapshotResultMessage): string {
        return JSON.stringify(uncast(value, r("GetScreenSnapshotResultMessage")), null, 2);
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

    public static toGitHubHostsUpdatedMessage(json: string): GitHubHostsUpdatedMessage {
        return cast(JSON.parse(json), r("GitHubHostsUpdatedMessage"));
    }

    public static gitHubHostsUpdatedMessageToJson(value: GitHubHostsUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("GitHubHostsUpdatedMessage")), null, 2);
    }

    public static toGitOperation(json: string): GitOperation {
        return cast(JSON.parse(json), r("GitOperation"));
    }

    public static gitOperationToJson(value: GitOperation): string {
        return JSON.stringify(uncast(value, r("GitOperation")), null, 2);
    }

    public static toGitOperationFinishedMessage(json: string): GitOperationFinishedMessage {
        return cast(JSON.parse(json), r("GitOperationFinishedMessage"));
    }

    public static gitOperationFinishedMessageToJson(value: GitOperationFinishedMessage): string {
        return JSON.stringify(uncast(value, r("GitOperationFinishedMessage")), null, 2);
    }

    public static toGitOperationKind(json: string): GitOperationKind {
        return cast(JSON.parse(json), r("GitOperationKind"));
    }

    public static gitOperationKindToJson(value: GitOperationKind): string {
        return JSON.stringify(uncast(value, r("GitOperationKind")), null, 2);
    }

    public static toGitOperationStartedMessage(json: string): GitOperationStartedMessage {
        return cast(JSON.parse(json), r("GitOperationStartedMessage"));
    }

    public static gitOperationStartedMessageToJson(value: GitOperationStartedMessage): string {
        return JSON.stringify(uncast(value, r("GitOperationStartedMessage")), null, 2);
    }

    public static toGitOperationStatus(json: string): GitOperationStatus {
        return cast(JSON.parse(json), r("GitOperationStatus"));
    }

    public static gitOperationStatusToJson(value: GitOperationStatus): string {
        return JSON.stringify(uncast(value, r("GitOperationStatus")), null, 2);
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

    public static toInspectPathMessage(json: string): InspectPathMessage {
        return cast(JSON.parse(json), r("InspectPathMessage"));
    }

    public static inspectPathMessageToJson(value: InspectPathMessage): string {
        return JSON.stringify(uncast(value, r("InspectPathMessage")), null, 2);
    }

    public static toInspectPathResultMessage(json: string): InspectPathResultMessage {
        return cast(JSON.parse(json), r("InspectPathResultMessage"));
    }

    public static inspectPathResultMessageToJson(value: InspectPathResultMessage): string {
        return JSON.stringify(uncast(value, r("InspectPathResultMessage")), null, 2);
    }

    public static toInstallPluginMessage(json: string): InstallPluginMessage {
        return cast(JSON.parse(json), r("InstallPluginMessage"));
    }

    public static installPluginMessageToJson(value: InstallPluginMessage): string {
        return JSON.stringify(uncast(value, r("InstallPluginMessage")), null, 2);
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

    public static toListDispatchesMessage(json: string): ListDispatchesMessage {
        return cast(JSON.parse(json), r("ListDispatchesMessage"));
    }

    public static listDispatchesMessageToJson(value: ListDispatchesMessage): string {
        return JSON.stringify(uncast(value, r("ListDispatchesMessage")), null, 2);
    }

    public static toListDispatchMessagesMessage(json: string): ListDispatchMessagesMessage {
        return cast(JSON.parse(json), r("ListDispatchMessagesMessage"));
    }

    public static listDispatchMessagesMessageToJson(value: ListDispatchMessagesMessage): string {
        return JSON.stringify(uncast(value, r("ListDispatchMessagesMessage")), null, 2);
    }

    public static toListEndpointsMessage(json: string): ListEndpointsMessage {
        return cast(JSON.parse(json), r("ListEndpointsMessage"));
    }

    public static listEndpointsMessageToJson(value: ListEndpointsMessage): string {
        return JSON.stringify(uncast(value, r("ListEndpointsMessage")), null, 2);
    }

    public static toListPluginsMessage(json: string): ListPluginsMessage {
        return cast(JSON.parse(json), r("ListPluginsMessage"));
    }

    public static listPluginsMessageToJson(value: ListPluginsMessage): string {
        return JSON.stringify(uncast(value, r("ListPluginsMessage")), null, 2);
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

    public static toMuteWorkspaceMessage(json: string): MuteWorkspaceMessage {
        return cast(JSON.parse(json), r("MuteWorkspaceMessage"));
    }

    public static muteWorkspaceMessageToJson(value: MuteWorkspaceMessage): string {
        return JSON.stringify(uncast(value, r("MuteWorkspaceMessage")), null, 2);
    }

    public static toOpenBrowserMessage(json: string): OpenBrowserMessage {
        return cast(JSON.parse(json), r("OpenBrowserMessage"));
    }

    public static openBrowserMessageToJson(value: OpenBrowserMessage): string {
        return JSON.stringify(uncast(value, r("OpenBrowserMessage")), null, 2);
    }

    public static toOpenMarkdownMessage(json: string): OpenMarkdownMessage {
        return cast(JSON.parse(json), r("OpenMarkdownMessage"));
    }

    public static openMarkdownMessageToJson(value: OpenMarkdownMessage): string {
        return JSON.stringify(uncast(value, r("OpenMarkdownMessage")), null, 2);
    }

    public static toPathInspection(json: string): PathInspection {
        return cast(JSON.parse(json), r("PathInspection"));
    }

    public static pathInspectionToJson(value: PathInspection): string {
        return JSON.stringify(uncast(value, r("PathInspection")), null, 2);
    }

    public static toPluginActionResultMessage(json: string): PluginActionResultMessage {
        return cast(JSON.parse(json), r("PluginActionResultMessage"));
    }

    public static pluginActionResultMessageToJson(value: PluginActionResultMessage): string {
        return JSON.stringify(uncast(value, r("PluginActionResultMessage")), null, 2);
    }

    public static toPluginInfo(json: string): PluginInfo {
        return cast(JSON.parse(json), r("PluginInfo"));
    }

    public static pluginInfoToJson(value: PluginInfo): string {
        return JSON.stringify(uncast(value, r("PluginInfo")), null, 2);
    }

    public static toPluginIssue(json: string): PluginIssue {
        return cast(JSON.parse(json), r("PluginIssue"));
    }

    public static pluginIssueToJson(value: PluginIssue): string {
        return JSON.stringify(uncast(value, r("PluginIssue")), null, 2);
    }

    public static toPluginsUpdatedMessage(json: string): PluginsUpdatedMessage {
        return cast(JSON.parse(json), r("PluginsUpdatedMessage"));
    }

    public static pluginsUpdatedMessageToJson(value: PluginsUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("PluginsUpdatedMessage")), null, 2);
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

    public static toPRsUpdatedMessage(json: string): PRsUpdatedMessage {
        return cast(JSON.parse(json), r("PRsUpdatedMessage"));
    }

    public static pRsUpdatedMessageToJson(value: PRsUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("PRsUpdatedMessage")), null, 2);
    }

    public static toPRVisitedMessage(json: string): PRVisitedMessage {
        return cast(JSON.parse(json), r("PRVisitedMessage"));
    }

    public static pRVisitedMessageToJson(value: PRVisitedMessage): string {
        return JSON.stringify(uncast(value, r("PRVisitedMessage")), null, 2);
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

    public static toPtyResizedMessage(json: string): PtyResizedMessage {
        return cast(JSON.parse(json), r("PtyResizedMessage"));
    }

    public static ptyResizedMessageToJson(value: PtyResizedMessage): string {
        return JSON.stringify(uncast(value, r("PtyResizedMessage")), null, 2);
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

    public static toReadDispatchMessage(json: string): ReadDispatchMessage {
        return cast(JSON.parse(json), r("ReadDispatchMessage"));
    }

    public static readDispatchMessageToJson(value: ReadDispatchMessage): string {
        return JSON.stringify(uncast(value, r("ReadDispatchMessage")), null, 2);
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

    public static toRegisterWorkspaceMessage(json: string): RegisterWorkspaceMessage {
        return cast(JSON.parse(json), r("RegisterWorkspaceMessage"));
    }

    public static registerWorkspaceMessageToJson(value: RegisterWorkspaceMessage): string {
        return JSON.stringify(uncast(value, r("RegisterWorkspaceMessage")), null, 2);
    }

    public static toRemoveEndpointMessage(json: string): RemoveEndpointMessage {
        return cast(JSON.parse(json), r("RemoveEndpointMessage"));
    }

    public static removeEndpointMessageToJson(value: RemoveEndpointMessage): string {
        return JSON.stringify(uncast(value, r("RemoveEndpointMessage")), null, 2);
    }

    public static toRemovePluginMessage(json: string): RemovePluginMessage {
        return cast(JSON.parse(json), r("RemovePluginMessage"));
    }

    public static removePluginMessageToJson(value: RemovePluginMessage): string {
        return JSON.stringify(uncast(value, r("RemovePluginMessage")), null, 2);
    }

    public static toRenameResultMessage(json: string): RenameResultMessage {
        return cast(JSON.parse(json), r("RenameResultMessage"));
    }

    public static renameResultMessageToJson(value: RenameResultMessage): string {
        return JSON.stringify(uncast(value, r("RenameResultMessage")), null, 2);
    }

    public static toRenameSessionMessage(json: string): RenameSessionMessage {
        return cast(JSON.parse(json), r("RenameSessionMessage"));
    }

    public static renameSessionMessageToJson(value: RenameSessionMessage): string {
        return JSON.stringify(uncast(value, r("RenameSessionMessage")), null, 2);
    }

    public static toRenameWorkspaceMessage(json: string): RenameWorkspaceMessage {
        return cast(JSON.parse(json), r("RenameWorkspaceMessage"));
    }

    public static renameWorkspaceMessageToJson(value: RenameWorkspaceMessage): string {
        return JSON.stringify(uncast(value, r("RenameWorkspaceMessage")), null, 2);
    }

    public static toReplaySegment(json: string): ReplaySegment {
        return cast(JSON.parse(json), r("ReplaySegment"));
    }

    public static replaySegmentToJson(value: ReplaySegment): string {
        return JSON.stringify(uncast(value, r("ReplaySegment")), null, 2);
    }

    public static toRepoInfo(json: string): RepoInfo {
        return cast(JSON.parse(json), r("RepoInfo"));
    }

    public static repoInfoToJson(value: RepoInfo): string {
        return JSON.stringify(uncast(value, r("RepoInfo")), null, 2);
    }

    public static toReportDispatchMessage(json: string): ReportDispatchMessage {
        return cast(JSON.parse(json), r("ReportDispatchMessage"));
    }

    public static reportDispatchMessageToJson(value: ReportDispatchMessage): string {
        return JSON.stringify(uncast(value, r("ReportDispatchMessage")), null, 2);
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

    public static toResolveDispatchRequestMessage(json: string): ResolveDispatchRequestMessage {
        return cast(JSON.parse(json), r("ResolveDispatchRequestMessage"));
    }

    public static resolveDispatchRequestMessageToJson(value: ResolveDispatchRequestMessage): string {
        return JSON.stringify(uncast(value, r("ResolveDispatchRequestMessage")), null, 2);
    }

    public static toResponse(json: string): Response {
        return cast(JSON.parse(json), r("Response"));
    }

    public static responseToJson(value: Response): string {
        return JSON.stringify(uncast(value, r("Response")), null, 2);
    }

    public static toReviewComment(json: string): ReviewComment {
        return cast(JSON.parse(json), r("ReviewComment"));
    }

    public static reviewCommentToJson(value: ReviewComment): string {
        return JSON.stringify(uncast(value, r("ReviewComment")), null, 2);
    }

    public static toReviewLoopDecision(json: string): ReviewLoopDecision {
        return cast(JSON.parse(json), r("ReviewLoopDecision"));
    }

    public static reviewLoopDecisionToJson(value: ReviewLoopDecision): string {
        return JSON.stringify(uncast(value, r("ReviewLoopDecision")), null, 2);
    }

    public static toReviewLoopInteraction(json: string): ReviewLoopInteraction {
        return cast(JSON.parse(json), r("ReviewLoopInteraction"));
    }

    public static reviewLoopInteractionToJson(value: ReviewLoopInteraction): string {
        return JSON.stringify(uncast(value, r("ReviewLoopInteraction")), null, 2);
    }

    public static toReviewLoopInteractionStatus(json: string): ReviewLoopInteractionStatus {
        return cast(JSON.parse(json), r("ReviewLoopInteractionStatus"));
    }

    public static reviewLoopInteractionStatusToJson(value: ReviewLoopInteractionStatus): string {
        return JSON.stringify(uncast(value, r("ReviewLoopInteractionStatus")), null, 2);
    }

    public static toReviewLoopIteration(json: string): ReviewLoopIteration {
        return cast(JSON.parse(json), r("ReviewLoopIteration"));
    }

    public static reviewLoopIterationToJson(value: ReviewLoopIteration): string {
        return JSON.stringify(uncast(value, r("ReviewLoopIteration")), null, 2);
    }

    public static toReviewLoopIterationStatus(json: string): ReviewLoopIterationStatus {
        return cast(JSON.parse(json), r("ReviewLoopIterationStatus"));
    }

    public static reviewLoopIterationStatusToJson(value: ReviewLoopIterationStatus): string {
        return JSON.stringify(uncast(value, r("ReviewLoopIterationStatus")), null, 2);
    }

    public static toReviewLoopResultMessage(json: string): ReviewLoopResultMessage {
        return cast(JSON.parse(json), r("ReviewLoopResultMessage"));
    }

    public static reviewLoopResultMessageToJson(value: ReviewLoopResultMessage): string {
        return JSON.stringify(uncast(value, r("ReviewLoopResultMessage")), null, 2);
    }

    public static toReviewLoopRun(json: string): ReviewLoopRun {
        return cast(JSON.parse(json), r("ReviewLoopRun"));
    }

    public static reviewLoopRunToJson(value: ReviewLoopRun): string {
        return JSON.stringify(uncast(value, r("ReviewLoopRun")), null, 2);
    }

    public static toReviewLoopRunStatus(json: string): ReviewLoopRunStatus {
        return cast(JSON.parse(json), r("ReviewLoopRunStatus"));
    }

    public static reviewLoopRunStatusToJson(value: ReviewLoopRunStatus): string {
        return JSON.stringify(uncast(value, r("ReviewLoopRunStatus")), null, 2);
    }

    public static toReviewLoopState(json: string): ReviewLoopState {
        return cast(JSON.parse(json), r("ReviewLoopState"));
    }

    public static reviewLoopStateToJson(value: ReviewLoopState): string {
        return JSON.stringify(uncast(value, r("ReviewLoopState")), null, 2);
    }

    public static toReviewLoopStatus(json: string): ReviewLoopStatus {
        return cast(JSON.parse(json), r("ReviewLoopStatus"));
    }

    public static reviewLoopStatusToJson(value: ReviewLoopStatus): string {
        return JSON.stringify(uncast(value, r("ReviewLoopStatus")), null, 2);
    }

    public static toReviewLoopUpdatedMessage(json: string): ReviewLoopUpdatedMessage {
        return cast(JSON.parse(json), r("ReviewLoopUpdatedMessage"));
    }

    public static reviewLoopUpdatedMessageToJson(value: ReviewLoopUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("ReviewLoopUpdatedMessage")), null, 2);
    }

    public static toReviewState(json: string): ReviewState {
        return cast(JSON.parse(json), r("ReviewState"));
    }

    public static reviewStateToJson(value: ReviewState): string {
        return JSON.stringify(uncast(value, r("ReviewState")), null, 2);
    }

    public static toSendDispatchMessage(json: string): SendDispatchMessage {
        return cast(JSON.parse(json), r("SendDispatchMessage"));
    }

    public static sendDispatchMessageToJson(value: SendDispatchMessage): string {
        return JSON.stringify(uncast(value, r("SendDispatchMessage")), null, 2);
    }

    public static toSession(json: string): Session {
        return cast(JSON.parse(json), r("Session"));
    }

    public static sessionToJson(value: Session): string {
        return JSON.stringify(uncast(value, r("Session")), null, 2);
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

    public static toSessionSelectedMessage(json: string): SessionSelectedMessage {
        return cast(JSON.parse(json), r("SessionSelectedMessage"));
    }

    public static sessionSelectedMessageToJson(value: SessionSelectedMessage): string {
        return JSON.stringify(uncast(value, r("SessionSelectedMessage")), null, 2);
    }

    public static toSessionState(json: string): WorkspaceStatus {
        return cast(JSON.parse(json), r("WorkspaceStatus"));
    }

    public static sessionStateToJson(value: WorkspaceStatus): string {
        return JSON.stringify(uncast(value, r("WorkspaceStatus")), null, 2);
    }

    public static toSessionStateChangedMessage(json: string): SessionStateChangedMessage {
        return cast(JSON.parse(json), r("SessionStateChangedMessage"));
    }

    public static sessionStateChangedMessageToJson(value: SessionStateChangedMessage): string {
        return JSON.stringify(uncast(value, r("SessionStateChangedMessage")), null, 2);
    }

    public static toSessionsUpdatedMessage(json: string): SessionsUpdatedMessage {
        return cast(JSON.parse(json), r("SessionsUpdatedMessage"));
    }

    public static sessionsUpdatedMessageToJson(value: SessionsUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("SessionsUpdatedMessage")), null, 2);
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

    public static toSetChiefOfStaffMessage(json: string): SetChiefOfStaffMessage {
        return cast(JSON.parse(json), r("SetChiefOfStaffMessage"));
    }

    public static setChiefOfStaffMessageToJson(value: SetChiefOfStaffMessage): string {
        return JSON.stringify(uncast(value, r("SetChiefOfStaffMessage")), null, 2);
    }

    public static toSetEndpointRemoteWebMessage(json: string): SetEndpointRemoteWebMessage {
        return cast(JSON.parse(json), r("SetEndpointRemoteWebMessage"));
    }

    public static setEndpointRemoteWebMessageToJson(value: SetEndpointRemoteWebMessage): string {
        return JSON.stringify(uncast(value, r("SetEndpointRemoteWebMessage")), null, 2);
    }

    public static toSetPluginPriorityMessage(json: string): SetPluginPriorityMessage {
        return cast(JSON.parse(json), r("SetPluginPriorityMessage"));
    }

    public static setPluginPriorityMessageToJson(value: SetPluginPriorityMessage): string {
        return JSON.stringify(uncast(value, r("SetPluginPriorityMessage")), null, 2);
    }

    public static toSetReviewLoopIterationLimitMessage(json: string): SetReviewLoopIterationLimitMessage {
        return cast(JSON.parse(json), r("SetReviewLoopIterationLimitMessage"));
    }

    public static setReviewLoopIterationLimitMessageToJson(value: SetReviewLoopIterationLimitMessage): string {
        return JSON.stringify(uncast(value, r("SetReviewLoopIterationLimitMessage")), null, 2);
    }

    public static toSetSessionResumeIDMessage(json: string): SetSessionResumeIDMessage {
        return cast(JSON.parse(json), r("SetSessionResumeIDMessage"));
    }

    public static setSessionResumeIDMessageToJson(value: SetSessionResumeIDMessage): string {
        return JSON.stringify(uncast(value, r("SetSessionResumeIDMessage")), null, 2);
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

    public static toSetWorkspaceRankMessage(json: string): SetWorkspaceRankMessage {
        return cast(JSON.parse(json), r("SetWorkspaceRankMessage"));
    }

    public static setWorkspaceRankMessageToJson(value: SetWorkspaceRankMessage): string {
        return JSON.stringify(uncast(value, r("SetWorkspaceRankMessage")), null, 2);
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

    public static toStartReviewLoopMessage(json: string): StartReviewLoopMessage {
        return cast(JSON.parse(json), r("StartReviewLoopMessage"));
    }

    public static startReviewLoopMessageToJson(value: StartReviewLoopMessage): string {
        return JSON.stringify(uncast(value, r("StartReviewLoopMessage")), null, 2);
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

    public static toStopReviewLoopMessage(json: string): StopReviewLoopMessage {
        return cast(JSON.parse(json), r("StopReviewLoopMessage"));
    }

    public static stopReviewLoopMessageToJson(value: StopReviewLoopMessage): string {
        return JSON.stringify(uncast(value, r("StopReviewLoopMessage")), null, 2);
    }

    public static toSubscribeGitStatusMessage(json: string): SubscribeGitStatusMessage {
        return cast(JSON.parse(json), r("SubscribeGitStatusMessage"));
    }

    public static subscribeGitStatusMessageToJson(value: SubscribeGitStatusMessage): string {
        return JSON.stringify(uncast(value, r("SubscribeGitStatusMessage")), null, 2);
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

    public static toUnregisterWorkspaceMessage(json: string): UnregisterWorkspaceMessage {
        return cast(JSON.parse(json), r("UnregisterWorkspaceMessage"));
    }

    public static unregisterWorkspaceMessageToJson(value: UnregisterWorkspaceMessage): string {
        return JSON.stringify(uncast(value, r("UnregisterWorkspaceMessage")), null, 2);
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

    public static toUpdateEndpointMessage(json: string): UpdateEndpointMessage {
        return cast(JSON.parse(json), r("UpdateEndpointMessage"));
    }

    public static updateEndpointMessageToJson(value: UpdateEndpointMessage): string {
        return JSON.stringify(uncast(value, r("UpdateEndpointMessage")), null, 2);
    }

    public static toWakeDispatchAgentMessage(json: string): WakeDispatchAgentMessage {
        return cast(JSON.parse(json), r("WakeDispatchAgentMessage"));
    }

    public static wakeDispatchAgentMessageToJson(value: WakeDispatchAgentMessage): string {
        return JSON.stringify(uncast(value, r("WakeDispatchAgentMessage")), null, 2);
    }

    public static toWakeDispatchAgentResultMessage(json: string): WakeDispatchAgentResultMessage {
        return cast(JSON.parse(json), r("WakeDispatchAgentResultMessage"));
    }

    public static wakeDispatchAgentResultMessageToJson(value: WakeDispatchAgentResultMessage): string {
        return JSON.stringify(uncast(value, r("WakeDispatchAgentResultMessage")), null, 2);
    }

    public static toWebSocketEvent(json: string): WebSocketEvent {
        return cast(JSON.parse(json), r("WebSocketEvent"));
    }

    public static webSocketEventToJson(value: WebSocketEvent): string {
        return JSON.stringify(uncast(value, r("WebSocketEvent")), null, 2);
    }

    public static toWorkspace(json: string): Workspace {
        return cast(JSON.parse(json), r("Workspace"));
    }

    public static workspaceToJson(value: Workspace): string {
        return JSON.stringify(uncast(value, r("Workspace")), null, 2);
    }

    public static toWorkspaceContext(json: string): WorkspaceContext {
        return cast(JSON.parse(json), r("WorkspaceContext"));
    }

    public static workspaceContextToJson(value: WorkspaceContext): string {
        return JSON.stringify(uncast(value, r("WorkspaceContext")), null, 2);
    }

    public static toWorkspaceContextChangedMessage(json: string): WorkspaceContextChangedMessage {
        return cast(JSON.parse(json), r("WorkspaceContextChangedMessage"));
    }

    public static workspaceContextChangedMessageToJson(value: WorkspaceContextChangedMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceContextChangedMessage")), null, 2);
    }

    public static toWorkspaceContextCheckoutMessage(json: string): WorkspaceContextCheckoutMessage {
        return cast(JSON.parse(json), r("WorkspaceContextCheckoutMessage"));
    }

    public static workspaceContextCheckoutMessageToJson(value: WorkspaceContextCheckoutMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceContextCheckoutMessage")), null, 2);
    }

    public static toWorkspaceContextCompactMessage(json: string): WorkspaceContextCompactMessage {
        return cast(JSON.parse(json), r("WorkspaceContextCompactMessage"));
    }

    public static workspaceContextCompactMessageToJson(value: WorkspaceContextCompactMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceContextCompactMessage")), null, 2);
    }

    public static toWorkspaceContextListMessage(json: string): WorkspaceContextListMessage {
        return cast(JSON.parse(json), r("WorkspaceContextListMessage"));
    }

    public static workspaceContextListMessageToJson(value: WorkspaceContextListMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceContextListMessage")), null, 2);
    }

    public static toWorkspaceContextListResultMessage(json: string): WorkspaceContextListResultMessage {
        return cast(JSON.parse(json), r("WorkspaceContextListResultMessage"));
    }

    public static workspaceContextListResultMessageToJson(value: WorkspaceContextListResultMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceContextListResultMessage")), null, 2);
    }

    public static toWorkspaceContextMaintenanceAction(json: string): WorkspaceContextMaintenanceAction {
        return cast(JSON.parse(json), r("WorkspaceContextMaintenanceAction"));
    }

    public static workspaceContextMaintenanceActionToJson(value: WorkspaceContextMaintenanceAction): string {
        return JSON.stringify(uncast(value, r("WorkspaceContextMaintenanceAction")), null, 2);
    }

    public static toWorkspaceContextMaintenanceResult(json: string): WorkspaceContextMaintenanceResult {
        return cast(JSON.parse(json), r("WorkspaceContextMaintenanceResult"));
    }

    public static workspaceContextMaintenanceResultToJson(value: WorkspaceContextMaintenanceResult): string {
        return JSON.stringify(uncast(value, r("WorkspaceContextMaintenanceResult")), null, 2);
    }

    public static toWorkspaceContextResult(json: string): WorkspaceContextResult {
        return cast(JSON.parse(json), r("WorkspaceContextResult"));
    }

    public static workspaceContextResultToJson(value: WorkspaceContextResult): string {
        return JSON.stringify(uncast(value, r("WorkspaceContextResult")), null, 2);
    }

    public static toWorkspaceContextResultMessage(json: string): WorkspaceContextResultMessage {
        return cast(JSON.parse(json), r("WorkspaceContextResultMessage"));
    }

    public static workspaceContextResultMessageToJson(value: WorkspaceContextResultMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceContextResultMessage")), null, 2);
    }

    public static toWorkspaceContextRollbackMessage(json: string): WorkspaceContextRollbackMessage {
        return cast(JSON.parse(json), r("WorkspaceContextRollbackMessage"));
    }

    public static workspaceContextRollbackMessageToJson(value: WorkspaceContextRollbackMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceContextRollbackMessage")), null, 2);
    }

    public static toWorkspaceContextStatusMessage(json: string): WorkspaceContextStatusMessage {
        return cast(JSON.parse(json), r("WorkspaceContextStatusMessage"));
    }

    public static workspaceContextStatusMessageToJson(value: WorkspaceContextStatusMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceContextStatusMessage")), null, 2);
    }

    public static toWorkspaceContextUpdateMessage(json: string): WorkspaceContextUpdateMessage {
        return cast(JSON.parse(json), r("WorkspaceContextUpdateMessage"));
    }

    public static workspaceContextUpdateMessageToJson(value: WorkspaceContextUpdateMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceContextUpdateMessage")), null, 2);
    }

    public static toWorkspaceLayout(json: string): WorkspaceLayout {
        return cast(JSON.parse(json), r("WorkspaceLayout"));
    }

    public static workspaceLayoutToJson(value: WorkspaceLayout): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayout")), null, 2);
    }

    public static toWorkspaceLayoutActionResultMessage(json: string): WorkspaceLayoutActionResultMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutActionResultMessage"));
    }

    public static workspaceLayoutActionResultMessageToJson(value: WorkspaceLayoutActionResultMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutActionResultMessage")), null, 2);
    }

    public static toWorkspaceLayoutAddSessionPaneMessage(json: string): WorkspaceLayoutAddSessionPaneMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutAddSessionPaneMessage"));
    }

    public static workspaceLayoutAddSessionPaneMessageToJson(value: WorkspaceLayoutAddSessionPaneMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutAddSessionPaneMessage")), null, 2);
    }

    public static toWorkspaceLayoutClosePaneMessage(json: string): WorkspaceLayoutClosePaneMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutClosePaneMessage"));
    }

    public static workspaceLayoutClosePaneMessageToJson(value: WorkspaceLayoutClosePaneMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutClosePaneMessage")), null, 2);
    }

    public static toWorkspaceLayoutDockEdge(json: string): WorkspaceLayoutDockEdge {
        return cast(JSON.parse(json), r("WorkspaceLayoutDockEdge"));
    }

    public static workspaceLayoutDockEdgeToJson(value: WorkspaceLayoutDockEdge): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutDockEdge")), null, 2);
    }

    public static toWorkspaceLayoutDockTileMessage(json: string): WorkspaceLayoutDockTileMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutDockTileMessage"));
    }

    public static workspaceLayoutDockTileMessageToJson(value: WorkspaceLayoutDockTileMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutDockTileMessage")), null, 2);
    }

    public static toWorkspaceLayoutFocusPaneMessage(json: string): WorkspaceLayoutFocusPaneMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutFocusPaneMessage"));
    }

    public static workspaceLayoutFocusPaneMessageToJson(value: WorkspaceLayoutFocusPaneMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutFocusPaneMessage")), null, 2);
    }

    public static toWorkspaceLayoutGetMessage(json: string): WorkspaceLayoutGetMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutGetMessage"));
    }

    public static workspaceLayoutGetMessageToJson(value: WorkspaceLayoutGetMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutGetMessage")), null, 2);
    }

    public static toWorkspaceLayoutMessage(json: string): WorkspaceLayoutMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutMessage"));
    }

    public static workspaceLayoutMessageToJson(value: WorkspaceLayoutMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutMessage")), null, 2);
    }

    public static toWorkspaceLayoutMoveLeafMessage(json: string): WorkspaceLayoutMoveLeafMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutMoveLeafMessage"));
    }

    public static workspaceLayoutMoveLeafMessageToJson(value: WorkspaceLayoutMoveLeafMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutMoveLeafMessage")), null, 2);
    }

    public static toWorkspaceLayoutMoveLeafToNewWorkspaceMessage(json: string): WorkspaceLayoutMoveLeafToNewWorkspaceMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutMoveLeafToNewWorkspaceMessage"));
    }

    public static workspaceLayoutMoveLeafToNewWorkspaceMessageToJson(value: WorkspaceLayoutMoveLeafToNewWorkspaceMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutMoveLeafToNewWorkspaceMessage")), null, 2);
    }

    public static toWorkspaceLayoutMoveLeafToWorkspaceMessage(json: string): WorkspaceLayoutMoveLeafToWorkspaceMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutMoveLeafToWorkspaceMessage"));
    }

    public static workspaceLayoutMoveLeafToWorkspaceMessageToJson(value: WorkspaceLayoutMoveLeafToWorkspaceMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutMoveLeafToWorkspaceMessage")), null, 2);
    }

    public static toWorkspaceLayoutPane(json: string): WorkspaceLayoutPane {
        return cast(JSON.parse(json), r("WorkspaceLayoutPane"));
    }

    public static workspaceLayoutPaneToJson(value: WorkspaceLayoutPane): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutPane")), null, 2);
    }

    public static toWorkspaceLayoutPaneKind(json: string): WorkspaceLayoutPaneKind {
        return cast(JSON.parse(json), r("WorkspaceLayoutPaneKind"));
    }

    public static workspaceLayoutPaneKindToJson(value: WorkspaceLayoutPaneKind): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutPaneKind")), null, 2);
    }

    public static toWorkspaceLayoutPaneStatus(json: string): WorkspaceLayoutPaneStatus {
        return cast(JSON.parse(json), r("WorkspaceLayoutPaneStatus"));
    }

    public static workspaceLayoutPaneStatusToJson(value: WorkspaceLayoutPaneStatus): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutPaneStatus")), null, 2);
    }

    public static toWorkspaceLayoutRenamePaneMessage(json: string): WorkspaceLayoutRenamePaneMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutRenamePaneMessage"));
    }

    public static workspaceLayoutRenamePaneMessageToJson(value: WorkspaceLayoutRenamePaneMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutRenamePaneMessage")), null, 2);
    }

    public static toWorkspaceLayoutSetSplitRatioMessage(json: string): WorkspaceLayoutSetSplitRatioMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutSetSplitRatioMessage"));
    }

    public static workspaceLayoutSetSplitRatioMessageToJson(value: WorkspaceLayoutSetSplitRatioMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutSetSplitRatioMessage")), null, 2);
    }

    public static toWorkspaceLayoutSplitDirection(json: string): WorkspaceLayoutSplitDirection {
        return cast(JSON.parse(json), r("WorkspaceLayoutSplitDirection"));
    }

    public static workspaceLayoutSplitDirectionToJson(value: WorkspaceLayoutSplitDirection): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutSplitDirection")), null, 2);
    }

    public static toWorkspaceLayoutUndockTileMessage(json: string): WorkspaceLayoutUndockTileMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutUndockTileMessage"));
    }

    public static workspaceLayoutUndockTileMessageToJson(value: WorkspaceLayoutUndockTileMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutUndockTileMessage")), null, 2);
    }

    public static toWorkspaceLayoutUpdatedMessage(json: string): WorkspaceLayoutUpdatedMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutUpdatedMessage"));
    }

    public static workspaceLayoutUpdatedMessageToJson(value: WorkspaceLayoutUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutUpdatedMessage")), null, 2);
    }

    public static toWorkspaceLayoutUpdateTileMessage(json: string): WorkspaceLayoutUpdateTileMessage {
        return cast(JSON.parse(json), r("WorkspaceLayoutUpdateTileMessage"));
    }

    public static workspaceLayoutUpdateTileMessageToJson(value: WorkspaceLayoutUpdateTileMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceLayoutUpdateTileMessage")), null, 2);
    }

    public static toWorkspaceRegisteredMessage(json: string): WorkspaceRegisteredMessage {
        return cast(JSON.parse(json), r("WorkspaceRegisteredMessage"));
    }

    public static workspaceRegisteredMessageToJson(value: WorkspaceRegisteredMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceRegisteredMessage")), null, 2);
    }

    public static toWorkspaceSelectedMessage(json: string): WorkspaceSelectedMessage {
        return cast(JSON.parse(json), r("WorkspaceSelectedMessage"));
    }

    public static workspaceSelectedMessageToJson(value: WorkspaceSelectedMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceSelectedMessage")), null, 2);
    }

    public static toWorkspaceStateChangedMessage(json: string): WorkspaceStateChangedMessage {
        return cast(JSON.parse(json), r("WorkspaceStateChangedMessage"));
    }

    public static workspaceStateChangedMessageToJson(value: WorkspaceStateChangedMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceStateChangedMessage")), null, 2);
    }

    public static toWorkspaceStatus(json: string): WorkspaceStatus {
        return cast(JSON.parse(json), r("WorkspaceStatus"));
    }

    public static workspaceStatusToJson(value: WorkspaceStatus): string {
        return JSON.stringify(uncast(value, r("WorkspaceStatus")), null, 2);
    }

    public static toWorkspaceTileContentGetMessage(json: string): WorkspaceTileContentGetMessage {
        return cast(JSON.parse(json), r("WorkspaceTileContentGetMessage"));
    }

    public static workspaceTileContentGetMessageToJson(value: WorkspaceTileContentGetMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceTileContentGetMessage")), null, 2);
    }

    public static toWorkspaceTileContentMessage(json: string): WorkspaceTileContentMessage {
        return cast(JSON.parse(json), r("WorkspaceTileContentMessage"));
    }

    public static workspaceTileContentMessageToJson(value: WorkspaceTileContentMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceTileContentMessage")), null, 2);
    }

    public static toWorkspaceUnregisteredMessage(json: string): WorkspaceUnregisteredMessage {
        return cast(JSON.parse(json), r("WorkspaceUnregisteredMessage"));
    }

    public static workspaceUnregisteredMessageToJson(value: WorkspaceUnregisteredMessage): string {
        return JSON.stringify(uncast(value, r("WorkspaceUnregisteredMessage")), null, 2);
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
    "AcknowledgeDispatchMessage": o([
        { json: "acknowledgement", js: "acknowledgement", typ: u(undefined, "") },
        { json: "cmd", js: "cmd", typ: r("AcknowledgeDispatchMessageCmd") },
        { json: "message_id", js: "message_id", typ: "" },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
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
    ], "any"),
    "AddEndpointMessage": o([
        { json: "cmd", js: "cmd", typ: r("AddEndpointMessageCmd") },
        { json: "name", js: "name", typ: "" },
        { json: "profile", js: "profile", typ: u(undefined, "") },
        { json: "ssh_target", js: "ssh_target", typ: "" },
    ], "any"),
    "AnswerReviewLoopMessage": o([
        { json: "answer", js: "answer", typ: "" },
        { json: "cmd", js: "cmd", typ: r("AnswerReviewLoopMessageCmd") },
        { json: "interaction_id", js: "interaction_id", typ: u(undefined, "") },
        { json: "loop_id", js: "loop_id", typ: "" },
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
        { json: "replay_segments", js: "replay_segments", typ: u(undefined, a(r("ReplaySegmentElement"))) },
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
    "ReplaySegmentElement": o([
        { json: "cols", js: "cols", typ: 0 },
        { json: "data", js: "data", typ: "" },
        { json: "rows", js: "rows", typ: 0 },
    ], "any"),
    "AttachSessionMessage": o([
        { json: "attach_policy", js: "attach_policy", typ: u(undefined, r("AttachPolicy")) },
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
    "BootstrapEndpointMessage": o([
        { json: "cmd", js: "cmd", typ: r("BootstrapEndpointMessageCmd") },
        { json: "endpoint_id", js: "endpoint_id", typ: "" },
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
        { json: "agent", js: "agent", typ: "" },
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "chief_of_staff", js: "chief_of_staff", typ: u(undefined, true) },
        { json: "directory", js: "directory", typ: "" },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "is_worktree", js: "is_worktree", typ: u(undefined, true) },
        { json: "label", js: "label", typ: "" },
        { json: "last_seen", js: "last_seen", typ: "" },
        { json: "main_repo", js: "main_repo", typ: u(undefined, "") },
        { json: "needs_review_after_long_run", js: "needs_review_after_long_run", typ: u(undefined, true) },
        { json: "recoverable", js: "recoverable", typ: u(undefined, true) },
        { json: "state", js: "state", typ: r("WorkspaceStatus") },
        { json: "state_since", js: "state_since", typ: "" },
        { json: "state_updated_at", js: "state_updated_at", typ: "" },
        { json: "todos", js: "todos", typ: u(undefined, a("")) },
        { json: "workspace_id", js: "workspace_id", typ: "" },
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
    "BrowseDirectoryMessage": o([
        { json: "cmd", js: "cmd", typ: r("BrowseDirectoryMessageCmd") },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "input_path", js: "input_path", typ: "" },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "BrowseDirectoryResultMessage": o([
        { json: "directory", js: "directory", typ: "" },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "entries", js: "entries", typ: a(r("EntryElement")) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("BrowseDirectoryResultMessageEvent") },
        { json: "home_path", js: "home_path", typ: u(undefined, "") },
        { json: "input_path", js: "input_path", typ: "" },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "EntryElement": o([
        { json: "name", js: "name", typ: "" },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "BrowserControlMessage": o([
        { json: "action", js: "action", typ: "" },
        { json: "cmd", js: "cmd", typ: r("BrowserControlMessageCmd") },
        { json: "params", js: "params", typ: u(undefined, "") },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
        { json: "selector", js: "selector", typ: u(undefined, "") },
        { json: "session_id", js: "session_id", typ: u(undefined, "") },
        { json: "text", js: "text", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: u(undefined, "") },
    ], "any"),
    "BrowserControlRequestMessage": o([
        { json: "action", js: "action", typ: "" },
        { json: "event", js: "event", typ: r("BrowserControlRequestMessageEvent") },
        { json: "params", js: "params", typ: u(undefined, "") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "selector", js: "selector", typ: u(undefined, "") },
        { json: "text", js: "text", typ: u(undefined, "") },
        { json: "tile_id", js: "tile_id", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "BrowserControlResponseMessage": o([
        { json: "data", js: "data", typ: u(undefined, "") },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("BrowserControlResponseMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "BrowserControlResultMessage": o([
        { json: "cmd", js: "cmd", typ: r("BrowserControlResultMessageCmd") },
        { json: "data", js: "data", typ: u(undefined, "") },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "ChiefOfStaffDispatch": o([
        { json: "actionable", js: "actionable", typ: u(undefined, true) },
        { json: "agent", js: "agent", typ: "" },
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "brief", js: "brief", typ: "" },
        { json: "chief_session_id", js: "chief_session_id", typ: "" },
        { json: "concise_summary", js: "concise_summary", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "directory", js: "directory", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "label", js: "label", typ: "" },
        { json: "latest_report", js: "latest_report", typ: u(undefined, "") },
        { json: "reported_at", js: "reported_at", typ: u(undefined, "") },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "status", js: "status", typ: "" },
        { json: "status_since", js: "status_since", typ: "" },
        { json: "structured_report", js: "structured_report", typ: u(undefined, r("Report")) },
        { json: "unread_message_count", js: "unread_message_count", typ: u(undefined, 0) },
        { json: "updated_at", js: "updated_at", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "Report": o([
        { json: "artifact", js: "artifact", typ: u(undefined, r("Artifact")) },
        { json: "constraints", js: "constraints", typ: u(undefined, a("")) },
        { json: "next_action", js: "next_action", typ: u(undefined, "") },
        { json: "next_actor", js: "next_actor", typ: u(undefined, "") },
        { json: "remaining_scope", js: "remaining_scope", typ: u(undefined, a("")) },
        { json: "report_type", js: "report_type", typ: r("DispatchReportType") },
        { json: "reported_at", js: "reported_at", typ: "" },
        { json: "request", js: "request", typ: u(undefined, r("Request")) },
        { json: "summary", js: "summary", typ: "" },
        { json: "verification", js: "verification", typ: u(undefined, a(r("VerificationElement"))) },
        { json: "work_state", js: "work_state", typ: r("DispatchWorkState") },
    ], "any"),
    "Artifact": o([
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "description", js: "description", typ: u(undefined, "") },
        { json: "dirty", js: "dirty", typ: u(undefined, true) },
        { json: "identity", js: "identity", typ: "" },
        { json: "revision", js: "revision", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: u(undefined, "") },
    ], "any"),
    "Request": o([
        { json: "consequence", js: "consequence", typ: u(undefined, "") },
        { json: "expected_responder", js: "expected_responder", typ: "" },
        { json: "question", js: "question", typ: "" },
        { json: "recommendation", js: "recommendation", typ: u(undefined, "") },
        { json: "resolution_link", js: "resolution_link", typ: u(undefined, "") },
        { json: "responded_at", js: "responded_at", typ: u(undefined, "") },
        { json: "responded_by", js: "responded_by", typ: u(undefined, "") },
        { json: "response", js: "response", typ: u(undefined, "") },
        { json: "status", js: "status", typ: r("DispatchRequestStatus") },
    ], "any"),
    "VerificationElement": o([
        { json: "actor", js: "actor", typ: "" },
        { json: "artifact_identity", js: "artifact_identity", typ: "" },
        { json: "current", js: "current", typ: u(undefined, true) },
        { json: "result", js: "result", typ: "" },
        { json: "target", js: "target", typ: "" },
        { json: "timestamp", js: "timestamp", typ: "" },
    ], "any"),
    "ChiefOfStaffDispatchesUpdatedMessage": o([
        { json: "dispatches", js: "dispatches", typ: a(r("ChiefOfStaffDispatchElement")) },
        { json: "event", js: "event", typ: r("ChiefOfStaffDispatchesUpdatedMessageEvent") },
    ], "any"),
    "ChiefOfStaffDispatchElement": o([
        { json: "actionable", js: "actionable", typ: u(undefined, true) },
        { json: "agent", js: "agent", typ: "" },
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "brief", js: "brief", typ: "" },
        { json: "chief_session_id", js: "chief_session_id", typ: "" },
        { json: "concise_summary", js: "concise_summary", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "directory", js: "directory", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "label", js: "label", typ: "" },
        { json: "latest_report", js: "latest_report", typ: u(undefined, "") },
        { json: "reported_at", js: "reported_at", typ: u(undefined, "") },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "status", js: "status", typ: "" },
        { json: "status_since", js: "status_since", typ: "" },
        { json: "structured_report", js: "structured_report", typ: u(undefined, r("Report")) },
        { json: "unread_message_count", js: "unread_message_count", typ: u(undefined, 0) },
        { json: "updated_at", js: "updated_at", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "ChiefOfStaffResultMessage": o([
        { json: "chief_of_staff", js: "chief_of_staff", typ: true },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("ChiefOfStaffResultMessageEvent") },
        { json: "previous_session_id", js: "previous_session_id", typ: u(undefined, "") },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "ClearSessionsMessage": o([
        { json: "cmd", js: "cmd", typ: r("ClearSessionsMessageCmd") },
    ], "any"),
    "ClearWarningsMessage": o([
        { json: "cmd", js: "cmd", typ: r("ClearWarningsMessageCmd") },
    ], "any"),
    "ClientHelloMessage": o([
        { json: "browser_host_token", js: "browser_host_token", typ: u(undefined, "") },
        { json: "capabilities", js: "capabilities", typ: a("") },
        { json: "client_kind", js: "client_kind", typ: "" },
        { json: "cmd", js: "cmd", typ: r("ClientHelloMessageCmd") },
        { json: "version", js: "version", typ: "" },
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
    "CreateWorktreeFromBranchMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "cmd", js: "cmd", typ: r("CreateWorktreeFromBranchMessageCmd") },
        { json: "main_repo", js: "main_repo", typ: "" },
        { json: "path", js: "path", typ: u(undefined, "") },
    ], "any"),
    "CreateWorktreeMessage": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "cmd", js: "cmd", typ: r("CreateWorktreeMessageCmd") },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "main_repo", js: "main_repo", typ: "" },
        { json: "path", js: "path", typ: u(undefined, "") },
        { json: "starting_from", js: "starting_from", typ: u(undefined, "") },
    ], "any"),
    "CreateWorktreeResultMessage": o([
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("CreateWorktreeResultMessageEvent") },
        { json: "path", js: "path", typ: u(undefined, "") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "DaemonWarning": o([
        { json: "code", js: "code", typ: "" },
        { json: "message", js: "message", typ: "" },
    ], "any"),
    "DelegateMessage": o([
        { json: "agent", js: "agent", typ: u(undefined, "") },
        { json: "brief", js: "brief", typ: "" },
        { json: "cmd", js: "cmd", typ: r("DelegateMessageCmd") },
        { json: "cwd", js: "cwd", typ: u(undefined, "") },
        { json: "label", js: "label", typ: u(undefined, "") },
        { json: "placement", js: "placement", typ: u(undefined, "") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: u(undefined, "") },
        { json: "worktree", js: "worktree", typ: u(undefined, r("DelegateMessageWorktree")) },
        { json: "yolo_mode", js: "yolo_mode", typ: u(undefined, true) },
    ], "any"),
    "DelegateMessageWorktree": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "path", js: "path", typ: u(undefined, "") },
        { json: "repo", js: "repo", typ: u(undefined, "") },
        { json: "starting_from", js: "starting_from", typ: u(undefined, "") },
    ], "any"),
    "DelegateResult": o([
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "directory", js: "directory", typ: "" },
        { json: "dispatch_id", js: "dispatch_id", typ: u(undefined, "") },
        { json: "placement", js: "placement", typ: "" },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
        { json: "worktree_created", js: "worktree_created", typ: u(undefined, true) },
    ], "any"),
    "DelegateResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("DelegateResultMessageEvent") },
        { json: "result", js: "result", typ: u(undefined, r("DelegateResultObject")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "DelegateResultObject": o([
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "directory", js: "directory", typ: "" },
        { json: "dispatch_id", js: "dispatch_id", typ: u(undefined, "") },
        { json: "placement", js: "placement", typ: "" },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
        { json: "worktree_created", js: "worktree_created", typ: u(undefined, true) },
    ], "any"),
    "DelegateWorktreeRequest": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "path", js: "path", typ: u(undefined, "") },
        { json: "repo", js: "repo", typ: u(undefined, "") },
        { json: "starting_from", js: "starting_from", typ: u(undefined, "") },
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
        { json: "cmd", js: "cmd", typ: r("GitOperationKind") },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "force", js: "force", typ: u(undefined, true) },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "DeleteWorktreeResultMessage": o([
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("DeleteWorktreeResultMessageEvent") },
        { json: "forceable", js: "forceable", typ: u(undefined, true) },
        { json: "path", js: "path", typ: "" },
        { json: "reason_kind", js: "reason_kind", typ: u(undefined, r("ReasonKind")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "DetachSessionMessage": o([
        { json: "cmd", js: "cmd", typ: r("DetachSessionMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "DirectoryEntry": o([
        { json: "name", js: "name", typ: "" },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "DispatchArtifact": o([
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "description", js: "description", typ: u(undefined, "") },
        { json: "dirty", js: "dirty", typ: u(undefined, true) },
        { json: "identity", js: "identity", typ: "" },
        { json: "revision", js: "revision", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: u(undefined, "") },
    ], "any"),
    "DispatchDecisionRequest": o([
        { json: "consequence", js: "consequence", typ: u(undefined, "") },
        { json: "expected_responder", js: "expected_responder", typ: "" },
        { json: "question", js: "question", typ: "" },
        { json: "recommendation", js: "recommendation", typ: u(undefined, "") },
        { json: "resolution_link", js: "resolution_link", typ: u(undefined, "") },
        { json: "responded_at", js: "responded_at", typ: u(undefined, "") },
        { json: "responded_by", js: "responded_by", typ: u(undefined, "") },
        { json: "response", js: "response", typ: u(undefined, "") },
        { json: "status", js: "status", typ: r("DispatchRequestStatus") },
    ], "any"),
    "DispatchMessage": o([
        { json: "acknowledged_at", js: "acknowledged_at", typ: u(undefined, "") },
        { json: "acknowledgement", js: "acknowledgement", typ: u(undefined, "") },
        { json: "content", js: "content", typ: "" },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "dispatch_id", js: "dispatch_id", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "read_at", js: "read_at", typ: u(undefined, "") },
        { json: "sender_session_id", js: "sender_session_id", typ: "" },
        { json: "target_session_id", js: "target_session_id", typ: "" },
    ], "any"),
    "DispatchReport": o([
        { json: "artifact", js: "artifact", typ: u(undefined, r("Artifact")) },
        { json: "constraints", js: "constraints", typ: u(undefined, a("")) },
        { json: "next_action", js: "next_action", typ: u(undefined, "") },
        { json: "next_actor", js: "next_actor", typ: u(undefined, "") },
        { json: "remaining_scope", js: "remaining_scope", typ: u(undefined, a("")) },
        { json: "report_type", js: "report_type", typ: r("DispatchReportType") },
        { json: "reported_at", js: "reported_at", typ: "" },
        { json: "request", js: "request", typ: u(undefined, r("Request")) },
        { json: "summary", js: "summary", typ: "" },
        { json: "verification", js: "verification", typ: u(undefined, a(r("VerificationElement"))) },
        { json: "work_state", js: "work_state", typ: r("DispatchWorkState") },
    ], "any"),
    "DispatchVerification": o([
        { json: "actor", js: "actor", typ: "" },
        { json: "artifact_identity", js: "artifact_identity", typ: "" },
        { json: "current", js: "current", typ: u(undefined, true) },
        { json: "result", js: "result", typ: "" },
        { json: "target", js: "target", typ: "" },
        { json: "timestamp", js: "timestamp", typ: "" },
    ], "any"),
    "EndpointActionResultMessage": o([
        { json: "action", js: "action", typ: "" },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("EndpointActionResultMessageEvent") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "EndpointCapabilities": o([
        { json: "agents_available", js: "agents_available", typ: a("") },
        { json: "daemon_instance_id", js: "daemon_instance_id", typ: u(undefined, "") },
        { json: "projects_directory", js: "projects_directory", typ: u(undefined, "") },
        { json: "protocol_version", js: "protocol_version", typ: "" },
        { json: "pty_backend_mode", js: "pty_backend_mode", typ: u(undefined, "") },
        { json: "tailscale_auth_url", js: "tailscale_auth_url", typ: u(undefined, "") },
        { json: "tailscale_domain", js: "tailscale_domain", typ: u(undefined, "") },
        { json: "tailscale_enabled", js: "tailscale_enabled", typ: u(undefined, true) },
        { json: "tailscale_error", js: "tailscale_error", typ: u(undefined, "") },
        { json: "tailscale_status", js: "tailscale_status", typ: u(undefined, "") },
        { json: "tailscale_url", js: "tailscale_url", typ: u(undefined, "") },
    ], "any"),
    "EndpointInfo": o([
        { json: "capabilities", js: "capabilities", typ: u(undefined, r("Capabilities")) },
        { json: "enabled", js: "enabled", typ: u(undefined, true) },
        { json: "id", js: "id", typ: "" },
        { json: "name", js: "name", typ: "" },
        { json: "profile", js: "profile", typ: u(undefined, "") },
        { json: "session_count", js: "session_count", typ: u(undefined, 0) },
        { json: "ssh_target", js: "ssh_target", typ: "" },
        { json: "status", js: "status", typ: "" },
        { json: "status_message", js: "status_message", typ: u(undefined, "") },
    ], "any"),
    "Capabilities": o([
        { json: "agents_available", js: "agents_available", typ: a("") },
        { json: "daemon_instance_id", js: "daemon_instance_id", typ: u(undefined, "") },
        { json: "projects_directory", js: "projects_directory", typ: u(undefined, "") },
        { json: "protocol_version", js: "protocol_version", typ: "" },
        { json: "pty_backend_mode", js: "pty_backend_mode", typ: u(undefined, "") },
        { json: "tailscale_auth_url", js: "tailscale_auth_url", typ: u(undefined, "") },
        { json: "tailscale_domain", js: "tailscale_domain", typ: u(undefined, "") },
        { json: "tailscale_enabled", js: "tailscale_enabled", typ: u(undefined, true) },
        { json: "tailscale_error", js: "tailscale_error", typ: u(undefined, "") },
        { json: "tailscale_status", js: "tailscale_status", typ: u(undefined, "") },
        { json: "tailscale_url", js: "tailscale_url", typ: u(undefined, "") },
    ], "any"),
    "EndpointStatusChangedMessage": o([
        { json: "endpoint", js: "endpoint", typ: r("Endpoint") },
        { json: "event", js: "event", typ: r("EndpointStatusChangedMessageEvent") },
    ], "any"),
    "Endpoint": o([
        { json: "capabilities", js: "capabilities", typ: u(undefined, r("Capabilities")) },
        { json: "enabled", js: "enabled", typ: u(undefined, true) },
        { json: "id", js: "id", typ: "" },
        { json: "name", js: "name", typ: "" },
        { json: "profile", js: "profile", typ: u(undefined, "") },
        { json: "session_count", js: "session_count", typ: u(undefined, 0) },
        { json: "ssh_target", js: "ssh_target", typ: "" },
        { json: "status", js: "status", typ: "" },
        { json: "status_message", js: "status_message", typ: u(undefined, "") },
    ], "any"),
    "EndpointsUpdatedMessage": o([
        { json: "endpoints", js: "endpoints", typ: a(r("Endpoint")) },
        { json: "event", js: "event", typ: r("EndpointsUpdatedMessageEvent") },
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
    "GetDispatchMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetDispatchMessageCmd") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
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
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "limit", js: "limit", typ: u(undefined, 0) },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "GetRepoInfoMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetRepoInfoMessageCmd") },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "GetRepoInfoResultMessage": o([
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
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
    "GetReviewLoopRunMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetReviewLoopRunMessageCmd") },
        { json: "loop_id", js: "loop_id", typ: "" },
    ], "any"),
    "GetReviewLoopStateMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetReviewLoopStateMessageCmd") },
        { json: "session_id", js: "session_id", typ: "" },
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
    "GetScreenSnapshotMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetScreenSnapshotMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "GetScreenSnapshotResultMessage": o([
        { json: "cols", js: "cols", typ: u(undefined, 0) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("GetScreenSnapshotResultMessageEvent") },
        { json: "id", js: "id", typ: "" },
        { json: "last_seq", js: "last_seq", typ: u(undefined, 0) },
        { json: "rows", js: "rows", typ: u(undefined, 0) },
        { json: "running", js: "running", typ: u(undefined, true) },
        { json: "screen_cols", js: "screen_cols", typ: u(undefined, 0) },
        { json: "screen_cursor_visible", js: "screen_cursor_visible", typ: u(undefined, true) },
        { json: "screen_cursor_x", js: "screen_cursor_x", typ: u(undefined, 0) },
        { json: "screen_cursor_y", js: "screen_cursor_y", typ: u(undefined, 0) },
        { json: "screen_rows", js: "screen_rows", typ: u(undefined, 0) },
        { json: "screen_snapshot", js: "screen_snapshot", typ: u(undefined, "") },
        { json: "screen_snapshot_fresh", js: "screen_snapshot_fresh", typ: u(undefined, true) },
        { json: "success", js: "success", typ: true },
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
    "GitHubHostsUpdatedMessage": o([
        { json: "event", js: "event", typ: r("GitHubHostsUpdatedMessageEvent") },
        { json: "github_hosts", js: "github_hosts", typ: a("") },
    ], "any"),
    "GitOperation": o([
        { json: "duration_ms", js: "duration_ms", typ: u(undefined, 0) },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "finished_at", js: "finished_at", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "kind", js: "kind", typ: r("GitOperationKind") },
        { json: "path", js: "path", typ: u(undefined, "") },
        { json: "started_at", js: "started_at", typ: "" },
        { json: "status", js: "status", typ: r("GitOperationStatus") },
    ], "any"),
    "GitOperationFinishedMessage": o([
        { json: "event", js: "event", typ: r("GitOperationFinishedMessageEvent") },
        { json: "operation", js: "operation", typ: r("Operation") },
    ], "any"),
    "Operation": o([
        { json: "duration_ms", js: "duration_ms", typ: u(undefined, 0) },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "finished_at", js: "finished_at", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "kind", js: "kind", typ: r("GitOperationKind") },
        { json: "path", js: "path", typ: u(undefined, "") },
        { json: "started_at", js: "started_at", typ: "" },
        { json: "status", js: "status", typ: r("GitOperationStatus") },
    ], "any"),
    "GitOperationStartedMessage": o([
        { json: "event", js: "event", typ: r("GitOperationStartedMessageEvent") },
        { json: "operation", js: "operation", typ: r("Operation") },
    ], "any"),
    "GitStatusUpdateMessage": o([
        { json: "directory", js: "directory", typ: "" },
        { json: "duration_ms", js: "duration_ms", typ: u(undefined, 0) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("GitStatusUpdateMessageEvent") },
        { json: "limited", js: "limited", typ: u(undefined, true) },
        { json: "limited_reason", js: "limited_reason", typ: u(undefined, "") },
        { json: "mode", js: "mode", typ: u(undefined, "") },
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
        { json: "chief_of_staff_dispatches", js: "chief_of_staff_dispatches", typ: u(undefined, a(r("ChiefOfStaffDispatchElement"))) },
        { json: "daemon_instance_id", js: "daemon_instance_id", typ: u(undefined, "") },
        { json: "endpoints", js: "endpoints", typ: u(undefined, a(r("Endpoint"))) },
        { json: "event", js: "event", typ: r("InitialStateMessageEvent") },
        { json: "github_hosts", js: "github_hosts", typ: u(undefined, a("")) },
        { json: "protocol_version", js: "protocol_version", typ: u(undefined, "") },
        { json: "prs", js: "prs", typ: u(undefined, a(r("PRElement"))) },
        { json: "repos", js: "repos", typ: u(undefined, a(r("RepoElement"))) },
        { json: "sessions", js: "sessions", typ: u(undefined, a(r("SessionElement"))) },
        { json: "settings", js: "settings", typ: u(undefined, m("any")) },
        { json: "source_fingerprint", js: "source_fingerprint", typ: u(undefined, "") },
        { json: "warnings", js: "warnings", typ: u(undefined, a(r("WarningElement"))) },
        { json: "workspaces", js: "workspaces", typ: u(undefined, a(r("WorkspaceElement"))) },
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
    "WorkspaceElement": o([
        { json: "directory", js: "directory", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "layout", js: "layout", typ: u(undefined, r("Layout")) },
        { json: "muted", js: "muted", typ: true },
        { json: "rank", js: "rank", typ: "" },
        { json: "status", js: "status", typ: r("WorkspaceStatus") },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "Layout": o([
        { json: "active_pane_id", js: "active_pane_id", typ: "" },
        { json: "layout_json", js: "layout_json", typ: "" },
        { json: "panes", js: "panes", typ: a(r("PaneElement")) },
        { json: "updated_at", js: "updated_at", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "PaneElement": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "kind", js: "kind", typ: r("WorkspaceLayoutPaneKind") },
        { json: "pane_id", js: "pane_id", typ: "" },
        { json: "runtime_id", js: "runtime_id", typ: u(undefined, "") },
        { json: "session_id", js: "session_id", typ: u(undefined, "") },
        { json: "status", js: "status", typ: r("WorkspaceLayoutPaneStatus") },
        { json: "title", js: "title", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "InjectTestPRMessage": o([
        { json: "cmd", js: "cmd", typ: r("InjectTestPRMessageCmd") },
        { json: "pr", js: "pr", typ: r("PRElement") },
    ], "any"),
    "InjectTestSessionMessage": o([
        { json: "cmd", js: "cmd", typ: r("InjectTestSessionMessageCmd") },
        { json: "session", js: "session", typ: r("SessionElement") },
    ], "any"),
    "InspectPathMessage": o([
        { json: "cmd", js: "cmd", typ: r("InspectPathMessageCmd") },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "InspectPathResultMessage": o([
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("InspectPathResultMessageEvent") },
        { json: "inspection", js: "inspection", typ: u(undefined, r("Inspection")) },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "Inspection": o([
        { json: "exists", js: "exists", typ: true },
        { json: "home_path", js: "home_path", typ: u(undefined, "") },
        { json: "input_path", js: "input_path", typ: "" },
        { json: "is_directory", js: "is_directory", typ: true },
        { json: "repo_root", js: "repo_root", typ: u(undefined, "") },
        { json: "resolved_path", js: "resolved_path", typ: "" },
    ], "any"),
    "InstallPluginMessage": o([
        { json: "cmd", js: "cmd", typ: r("InstallPluginMessageCmd") },
        { json: "source", js: "source", typ: "" },
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
    "ListDispatchesMessage": o([
        { json: "cmd", js: "cmd", typ: r("ListDispatchesMessageCmd") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
    "ListDispatchMessagesMessage": o([
        { json: "cmd", js: "cmd", typ: r("ListDispatchMessagesMessageCmd") },
        { json: "dispatch_id", js: "dispatch_id", typ: u(undefined, "") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
        { json: "unread_only", js: "unread_only", typ: u(undefined, true) },
    ], "any"),
    "ListEndpointsMessage": o([
        { json: "cmd", js: "cmd", typ: r("ListEndpointsMessageCmd") },
    ], "any"),
    "ListPluginsMessage": o([
        { json: "cmd", js: "cmd", typ: r("ListPluginsMessageCmd") },
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
    "MutePRMessage": o([
        { json: "cmd", js: "cmd", typ: r("MutePRMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "MuteRepoMessage": o([
        { json: "cmd", js: "cmd", typ: r("MuteRepoMessageCmd") },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "MuteWorkspaceMessage": o([
        { json: "cmd", js: "cmd", typ: r("MuteWorkspaceMessageCmd") },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "OpenBrowserMessage": o([
        { json: "cmd", js: "cmd", typ: r("OpenBrowserMessageCmd") },
        { json: "session_id", js: "session_id", typ: u(undefined, "") },
        { json: "url", js: "url", typ: "" },
    ], "any"),
    "OpenMarkdownMessage": o([
        { json: "cmd", js: "cmd", typ: r("OpenMarkdownMessageCmd") },
        { json: "path", js: "path", typ: "" },
        { json: "session_id", js: "session_id", typ: u(undefined, "") },
    ], "any"),
    "PathInspection": o([
        { json: "exists", js: "exists", typ: true },
        { json: "home_path", js: "home_path", typ: u(undefined, "") },
        { json: "input_path", js: "input_path", typ: "" },
        { json: "is_directory", js: "is_directory", typ: true },
        { json: "repo_root", js: "repo_root", typ: u(undefined, "") },
        { json: "resolved_path", js: "resolved_path", typ: "" },
    ], "any"),
    "PluginActionResultMessage": o([
        { json: "action", js: "action", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("PluginActionResultMessageEvent") },
        { json: "name", js: "name", typ: u(undefined, "") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "PluginInfo": o([
        { json: "connected", js: "connected", typ: true },
        { json: "description", js: "description", typ: u(undefined, "") },
        { json: "dir", js: "dir", typ: "" },
        { json: "health_message", js: "health_message", typ: u(undefined, "") },
        { json: "health_status", js: "health_status", typ: u(undefined, "") },
        { json: "last_health_at", js: "last_health_at", typ: u(undefined, "") },
        { json: "name", js: "name", typ: "" },
        { json: "priority", js: "priority", typ: 0 },
        { json: "running", js: "running", typ: true },
        { json: "version", js: "version", typ: "" },
    ], "any"),
    "PluginIssue": o([
        { json: "error", js: "error", typ: "" },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "PluginsUpdatedMessage": o([
        { json: "event", js: "event", typ: r("PluginsUpdatedMessageEvent") },
        { json: "issues", js: "issues", typ: u(undefined, a(r("IssueElement"))) },
        { json: "plugins", js: "plugins", typ: a(r("PluginElement")) },
    ], "any"),
    "IssueElement": o([
        { json: "error", js: "error", typ: "" },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "PluginElement": o([
        { json: "connected", js: "connected", typ: true },
        { json: "description", js: "description", typ: u(undefined, "") },
        { json: "dir", js: "dir", typ: "" },
        { json: "health_message", js: "health_message", typ: u(undefined, "") },
        { json: "health_status", js: "health_status", typ: u(undefined, "") },
        { json: "last_health_at", js: "last_health_at", typ: u(undefined, "") },
        { json: "name", js: "name", typ: "" },
        { json: "priority", js: "priority", typ: 0 },
        { json: "running", js: "running", typ: true },
        { json: "version", js: "version", typ: "" },
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
    "PRsUpdatedMessage": o([
        { json: "event", js: "event", typ: r("PRsUpdatedMessageEvent") },
        { json: "prs", js: "prs", typ: u(undefined, a(r("PRElement"))) },
    ], "any"),
    "PRVisitedMessage": o([
        { json: "cmd", js: "cmd", typ: r("PRVisitedMessageCmd") },
        { json: "id", js: "id", typ: "" },
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
        { json: "source", js: "source", typ: u(undefined, "") },
    ], "any"),
    "PtyOutputMessage": o([
        { json: "data", js: "data", typ: "" },
        { json: "event", js: "event", typ: r("PtyOutputMessageEvent") },
        { json: "id", js: "id", typ: "" },
        { json: "seq", js: "seq", typ: 0 },
    ], "any"),
    "PtyResizedMessage": o([
        { json: "cols", js: "cols", typ: 0 },
        { json: "event", js: "event", typ: r("PtyResizedMessageEvent") },
        { json: "id", js: "id", typ: "" },
        { json: "rows", js: "rows", typ: 0 },
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
    "ReadDispatchMessage": o([
        { json: "cmd", js: "cmd", typ: r("ReadDispatchMessageCmd") },
        { json: "message_id", js: "message_id", typ: "" },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
    "RecentLocation": o([
        { json: "last_seen", js: "last_seen", typ: "" },
        { json: "path", js: "path", typ: "" },
        { json: "use_count", js: "use_count", typ: 0 },
    ], "any"),
    "RecentLocationsResultMessage": o([
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("RecentLocationsResultMessageEvent") },
        { json: "home_path", js: "home_path", typ: u(undefined, "") },
        { json: "recent_locations", js: "recent_locations", typ: a(r("RecentLocationElement")) },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "RecentLocationElement": o([
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
        { json: "agent", js: "agent", typ: u(undefined, "") },
        { json: "cmd", js: "cmd", typ: r("RegisterMessageCmd") },
        { json: "dir", js: "dir", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "label", js: "label", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "RegisterWorkspaceMessage": o([
        { json: "cmd", js: "cmd", typ: r("RegisterWorkspaceMessageCmd") },
        { json: "directory", js: "directory", typ: "" },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "RemoveEndpointMessage": o([
        { json: "cmd", js: "cmd", typ: r("RemoveEndpointMessageCmd") },
        { json: "endpoint_id", js: "endpoint_id", typ: "" },
    ], "any"),
    "RemovePluginMessage": o([
        { json: "cmd", js: "cmd", typ: r("RemovePluginMessageCmd") },
        { json: "name", js: "name", typ: "" },
    ], "any"),
    "RenameResultMessage": o([
        { json: "cmd", js: "cmd", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("RenameResultMessageEvent") },
        { json: "id", js: "id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "RenameSessionMessage": o([
        { json: "cmd", js: "cmd", typ: r("RenameSessionMessageCmd") },
        { json: "label", js: "label", typ: "" },
        { json: "session_id", js: "session_id", typ: "" },
    ], "any"),
    "RenameWorkspaceMessage": o([
        { json: "cmd", js: "cmd", typ: r("RenameWorkspaceMessageCmd") },
        { json: "title", js: "title", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "ReplaySegment": o([
        { json: "cols", js: "cols", typ: 0 },
        { json: "data", js: "data", typ: "" },
        { json: "rows", js: "rows", typ: 0 },
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
    "ReportDispatchMessage": o([
        { json: "cmd", js: "cmd", typ: r("ReportDispatchMessageCmd") },
        { json: "report", js: "report", typ: "" },
        { json: "source_session_id", js: "source_session_id", typ: "" },
        { json: "structured_report", js: "structured_report", typ: u(undefined, r("Report")) },
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
    "ResolveDispatchRequestMessage": o([
        { json: "cmd", js: "cmd", typ: r("ResolveDispatchRequestMessageCmd") },
        { json: "dispatch_id", js: "dispatch_id", typ: "" },
        { json: "resolution_link", js: "resolution_link", typ: u(undefined, "") },
        { json: "response", js: "response", typ: "" },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
    "Response": o([
        { json: "authors", js: "authors", typ: u(undefined, a(r("AuthorElement"))) },
        { json: "chief_of_staff_dispatch", js: "chief_of_staff_dispatch", typ: u(undefined, r("ChiefOfStaffDispatchElement")) },
        { json: "chief_of_staff_dispatches", js: "chief_of_staff_dispatches", typ: u(undefined, a(r("ChiefOfStaffDispatchElement"))) },
        { json: "data", js: "data", typ: u(undefined, "") },
        { json: "delegate_result", js: "delegate_result", typ: u(undefined, r("DelegateResultObject")) },
        { json: "dispatch_message", js: "dispatch_message", typ: u(undefined, r("DispatchMessageObject")) },
        { json: "dispatch_messages", js: "dispatch_messages", typ: u(undefined, a(r("DispatchMessageObject"))) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "ok", js: "ok", typ: true },
        { json: "prs", js: "prs", typ: u(undefined, a(r("PRElement"))) },
        { json: "repos", js: "repos", typ: u(undefined, a(r("RepoElement"))) },
        { json: "review_loop_run", js: "review_loop_run", typ: u(undefined, r("ReviewLoopRunObject")) },
        { json: "sessions", js: "sessions", typ: u(undefined, a(r("SessionElement"))) },
        { json: "workspace_context_maintenance_result", js: "workspace_context_maintenance_result", typ: u(undefined, r("WorkspaceContextMaintenanceResultObject")) },
        { json: "workspace_context_result", js: "workspace_context_result", typ: u(undefined, r("WorkspaceContextResultObject")) },
        { json: "workspace_contexts", js: "workspace_contexts", typ: u(undefined, a(r("WorkspaceContextElement"))) },
    ], "any"),
    "DispatchMessageObject": o([
        { json: "acknowledged_at", js: "acknowledged_at", typ: u(undefined, "") },
        { json: "acknowledgement", js: "acknowledgement", typ: u(undefined, "") },
        { json: "content", js: "content", typ: "" },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "dispatch_id", js: "dispatch_id", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "read_at", js: "read_at", typ: u(undefined, "") },
        { json: "sender_session_id", js: "sender_session_id", typ: "" },
        { json: "target_session_id", js: "target_session_id", typ: "" },
    ], "any"),
    "ReviewLoopRunObject": o([
        { json: "completed_at", js: "completed_at", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "custom_prompt", js: "custom_prompt", typ: u(undefined, "") },
        { json: "handoff_payload_json", js: "handoff_payload_json", typ: u(undefined, "") },
        { json: "iteration_count", js: "iteration_count", typ: 0 },
        { json: "iteration_limit", js: "iteration_limit", typ: 0 },
        { json: "iterations", js: "iterations", typ: u(undefined, a(r("Iteration"))) },
        { json: "last_decision", js: "last_decision", typ: u(undefined, r("ReviewLoopDecision")) },
        { json: "last_error", js: "last_error", typ: u(undefined, "") },
        { json: "last_result_summary", js: "last_result_summary", typ: u(undefined, "") },
        { json: "latest_iteration", js: "latest_iteration", typ: u(undefined, r("Iteration")) },
        { json: "loop_id", js: "loop_id", typ: "" },
        { json: "pending_interaction", js: "pending_interaction", typ: u(undefined, r("Interaction")) },
        { json: "pending_interaction_id", js: "pending_interaction_id", typ: u(undefined, "") },
        { json: "preset_id", js: "preset_id", typ: u(undefined, "") },
        { json: "repo_path", js: "repo_path", typ: "" },
        { json: "resolved_prompt", js: "resolved_prompt", typ: "" },
        { json: "source_session_id", js: "source_session_id", typ: "" },
        { json: "status", js: "status", typ: r("ReviewLoopRunStatus") },
        { json: "stop_reason", js: "stop_reason", typ: u(undefined, "") },
        { json: "updated_at", js: "updated_at", typ: "" },
    ], "any"),
    "Iteration": o([
        { json: "assistant_trace_json", js: "assistant_trace_json", typ: u(undefined, "") },
        { json: "blocking_reason", js: "blocking_reason", typ: u(undefined, "") },
        { json: "change_stats", js: "change_stats", typ: u(undefined, a(r("FileElement"))) },
        { json: "changes_made", js: "changes_made", typ: u(undefined, true) },
        { json: "completed_at", js: "completed_at", typ: u(undefined, "") },
        { json: "decision", js: "decision", typ: u(undefined, r("ReviewLoopDecision")) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "files_touched", js: "files_touched", typ: u(undefined, a("")) },
        { json: "id", js: "id", typ: "" },
        { json: "iteration_number", js: "iteration_number", typ: 0 },
        { json: "loop_id", js: "loop_id", typ: "" },
        { json: "result_text", js: "result_text", typ: u(undefined, "") },
        { json: "started_at", js: "started_at", typ: "" },
        { json: "status", js: "status", typ: r("ReviewLoopIterationStatus") },
        { json: "structured_output_json", js: "structured_output_json", typ: u(undefined, "") },
        { json: "suggested_next_focus", js: "suggested_next_focus", typ: u(undefined, "") },
        { json: "summary", js: "summary", typ: u(undefined, "") },
    ], "any"),
    "Interaction": o([
        { json: "answer", js: "answer", typ: u(undefined, "") },
        { json: "answered_at", js: "answered_at", typ: u(undefined, "") },
        { json: "consumed_at", js: "consumed_at", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "iteration_id", js: "iteration_id", typ: u(undefined, "") },
        { json: "kind", js: "kind", typ: "" },
        { json: "loop_id", js: "loop_id", typ: "" },
        { json: "question", js: "question", typ: "" },
        { json: "status", js: "status", typ: r("ReviewLoopInteractionStatus") },
    ], "any"),
    "WorkspaceContextMaintenanceResultObject": o([
        { json: "action", js: "action", typ: r("WorkspaceContextMaintenanceAction") },
        { json: "agent", js: "agent", typ: u(undefined, "") },
        { json: "agent_model", js: "agent_model", typ: u(undefined, "") },
        { json: "changed", js: "changed", typ: true },
        { json: "result_revision", js: "result_revision", typ: 0 },
        { json: "source_revision", js: "source_revision", typ: 0 },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceContextResultObject": o([
        { json: "canonical_revision", js: "canonical_revision", typ: 0 },
        { json: "modified", js: "modified", typ: true },
        { json: "path", js: "path", typ: "" },
        { json: "revision", js: "revision", typ: 0 },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "stale", js: "stale", typ: true },
        { json: "updated_at", js: "updated_at", typ: u(undefined, "") },
        { json: "updated_by_session_id", js: "updated_by_session_id", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceContextElement": o([
        { json: "content", js: "content", typ: "" },
        { json: "revision", js: "revision", typ: 0 },
        { json: "updated_at", js: "updated_at", typ: "" },
        { json: "updated_by_session_id", js: "updated_by_session_id", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
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
    ], "any"),
    "ReviewLoopInteraction": o([
        { json: "answer", js: "answer", typ: u(undefined, "") },
        { json: "answered_at", js: "answered_at", typ: u(undefined, "") },
        { json: "consumed_at", js: "consumed_at", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "iteration_id", js: "iteration_id", typ: u(undefined, "") },
        { json: "kind", js: "kind", typ: "" },
        { json: "loop_id", js: "loop_id", typ: "" },
        { json: "question", js: "question", typ: "" },
        { json: "status", js: "status", typ: r("ReviewLoopInteractionStatus") },
    ], "any"),
    "ReviewLoopIteration": o([
        { json: "assistant_trace_json", js: "assistant_trace_json", typ: u(undefined, "") },
        { json: "blocking_reason", js: "blocking_reason", typ: u(undefined, "") },
        { json: "change_stats", js: "change_stats", typ: u(undefined, a(r("FileElement"))) },
        { json: "changes_made", js: "changes_made", typ: u(undefined, true) },
        { json: "completed_at", js: "completed_at", typ: u(undefined, "") },
        { json: "decision", js: "decision", typ: u(undefined, r("ReviewLoopDecision")) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "files_touched", js: "files_touched", typ: u(undefined, a("")) },
        { json: "id", js: "id", typ: "" },
        { json: "iteration_number", js: "iteration_number", typ: 0 },
        { json: "loop_id", js: "loop_id", typ: "" },
        { json: "result_text", js: "result_text", typ: u(undefined, "") },
        { json: "started_at", js: "started_at", typ: "" },
        { json: "status", js: "status", typ: r("ReviewLoopIterationStatus") },
        { json: "structured_output_json", js: "structured_output_json", typ: u(undefined, "") },
        { json: "suggested_next_focus", js: "suggested_next_focus", typ: u(undefined, "") },
        { json: "summary", js: "summary", typ: u(undefined, "") },
    ], "any"),
    "ReviewLoopResultMessage": o([
        { json: "action", js: "action", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("ReviewLoopResultMessageEvent") },
        { json: "loop_id", js: "loop_id", typ: u(undefined, "") },
        { json: "review_loop_run", js: "review_loop_run", typ: u(undefined, r("ReviewLoopRunObject")) },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "ReviewLoopRun": o([
        { json: "completed_at", js: "completed_at", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "custom_prompt", js: "custom_prompt", typ: u(undefined, "") },
        { json: "handoff_payload_json", js: "handoff_payload_json", typ: u(undefined, "") },
        { json: "iteration_count", js: "iteration_count", typ: 0 },
        { json: "iteration_limit", js: "iteration_limit", typ: 0 },
        { json: "iterations", js: "iterations", typ: u(undefined, a(r("Iteration"))) },
        { json: "last_decision", js: "last_decision", typ: u(undefined, r("ReviewLoopDecision")) },
        { json: "last_error", js: "last_error", typ: u(undefined, "") },
        { json: "last_result_summary", js: "last_result_summary", typ: u(undefined, "") },
        { json: "latest_iteration", js: "latest_iteration", typ: u(undefined, r("Iteration")) },
        { json: "loop_id", js: "loop_id", typ: "" },
        { json: "pending_interaction", js: "pending_interaction", typ: u(undefined, r("Interaction")) },
        { json: "pending_interaction_id", js: "pending_interaction_id", typ: u(undefined, "") },
        { json: "preset_id", js: "preset_id", typ: u(undefined, "") },
        { json: "repo_path", js: "repo_path", typ: "" },
        { json: "resolved_prompt", js: "resolved_prompt", typ: "" },
        { json: "source_session_id", js: "source_session_id", typ: "" },
        { json: "status", js: "status", typ: r("ReviewLoopRunStatus") },
        { json: "stop_reason", js: "stop_reason", typ: u(undefined, "") },
        { json: "updated_at", js: "updated_at", typ: "" },
    ], "any"),
    "ReviewLoopState": o([
        { json: "advance_token", js: "advance_token", typ: "" },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "custom_prompt", js: "custom_prompt", typ: u(undefined, "") },
        { json: "iteration_count", js: "iteration_count", typ: 0 },
        { json: "iteration_limit", js: "iteration_limit", typ: 0 },
        { json: "last_advance_at", js: "last_advance_at", typ: u(undefined, "") },
        { json: "last_prompt_at", js: "last_prompt_at", typ: u(undefined, "") },
        { json: "last_user_input_at", js: "last_user_input_at", typ: u(undefined, "") },
        { json: "preset_id", js: "preset_id", typ: u(undefined, "") },
        { json: "resolved_prompt", js: "resolved_prompt", typ: "" },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "status", js: "status", typ: r("ReviewLoopStatus") },
        { json: "stop_reason", js: "stop_reason", typ: u(undefined, "") },
        { json: "stop_requested", js: "stop_requested", typ: true },
        { json: "updated_at", js: "updated_at", typ: "" },
    ], "any"),
    "ReviewLoopUpdatedMessage": o([
        { json: "event", js: "event", typ: r("ReviewLoopUpdatedMessageEvent") },
        { json: "review_loop_run", js: "review_loop_run", typ: u(undefined, r("ReviewLoopRunObject")) },
        { json: "session_id", js: "session_id", typ: "" },
    ], "any"),
    "ReviewState": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "repo_path", js: "repo_path", typ: "" },
        { json: "review_id", js: "review_id", typ: "" },
        { json: "viewed_files", js: "viewed_files", typ: a("") },
    ], "any"),
    "SendDispatchMessage": o([
        { json: "cmd", js: "cmd", typ: r("SendDispatchMessageCmd") },
        { json: "content", js: "content", typ: "" },
        { json: "dispatch_id", js: "dispatch_id", typ: "" },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
    "Session": o([
        { json: "agent", js: "agent", typ: "" },
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "chief_of_staff", js: "chief_of_staff", typ: u(undefined, true) },
        { json: "directory", js: "directory", typ: "" },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "is_worktree", js: "is_worktree", typ: u(undefined, true) },
        { json: "label", js: "label", typ: "" },
        { json: "last_seen", js: "last_seen", typ: "" },
        { json: "main_repo", js: "main_repo", typ: u(undefined, "") },
        { json: "needs_review_after_long_run", js: "needs_review_after_long_run", typ: u(undefined, true) },
        { json: "recoverable", js: "recoverable", typ: u(undefined, true) },
        { json: "state", js: "state", typ: r("WorkspaceStatus") },
        { json: "state_since", js: "state_since", typ: "" },
        { json: "state_updated_at", js: "state_updated_at", typ: "" },
        { json: "todos", js: "todos", typ: u(undefined, a("")) },
        { json: "workspace_id", js: "workspace_id", typ: "" },
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
    "SessionSelectedMessage": o([
        { json: "cmd", js: "cmd", typ: r("SessionSelectedMessageCmd") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "SessionStateChangedMessage": o([
        { json: "event", js: "event", typ: r("SessionStateChangedMessageEvent") },
        { json: "session", js: "session", typ: r("SessionElement") },
    ], "any"),
    "SessionsUpdatedMessage": o([
        { json: "event", js: "event", typ: r("SessionsUpdatedMessageEvent") },
        { json: "sessions", js: "sessions", typ: u(undefined, a(r("SessionElement"))) },
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
    "SetChiefOfStaffMessage": o([
        { json: "chief_of_staff", js: "chief_of_staff", typ: true },
        { json: "cmd", js: "cmd", typ: r("SetChiefOfStaffMessageCmd") },
        { json: "session_id", js: "session_id", typ: "" },
    ], "any"),
    "SetEndpointRemoteWebMessage": o([
        { json: "cmd", js: "cmd", typ: r("SetEndpointRemoteWebMessageCmd") },
        { json: "enabled", js: "enabled", typ: true },
        { json: "endpoint_id", js: "endpoint_id", typ: "" },
    ], "any"),
    "SetPluginPriorityMessage": o([
        { json: "cmd", js: "cmd", typ: r("SetPluginPriorityMessageCmd") },
        { json: "name", js: "name", typ: "" },
        { json: "priority", js: "priority", typ: 0 },
    ], "any"),
    "SetReviewLoopIterationLimitMessage": o([
        { json: "cmd", js: "cmd", typ: r("SetReviewLoopIterationLimitMessageCmd") },
        { json: "iteration_limit", js: "iteration_limit", typ: 0 },
        { json: "session_id", js: "session_id", typ: "" },
    ], "any"),
    "SetSessionResumeIDMessage": o([
        { json: "cmd", js: "cmd", typ: r("SetSessionResumeIDMessageCmd") },
        { json: "id", js: "id", typ: "" },
        { json: "resume_session_id", js: "resume_session_id", typ: "" },
    ], "any"),
    "SetSettingMessage": o([
        { json: "cmd", js: "cmd", typ: r("SetSettingMessageCmd") },
        { json: "key", js: "key", typ: "" },
        { json: "value", js: "value", typ: "" },
    ], "any"),
    "SettingsUpdatedMessage": o([
        { json: "changed_key", js: "changed_key", typ: u(undefined, "") },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("SettingsUpdatedMessageEvent") },
        { json: "settings", js: "settings", typ: u(undefined, m("any")) },
        { json: "success", js: "success", typ: u(undefined, true) },
    ], "any"),
    "SetWorkspaceRankMessage": o([
        { json: "cmd", js: "cmd", typ: r("SetWorkspaceRankMessageCmd") },
        { json: "next_workspace_id", js: "next_workspace_id", typ: u(undefined, "") },
        { json: "prev_workspace_id", js: "prev_workspace_id", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: "" },
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
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "executable", js: "executable", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "initial_prompt", js: "initial_prompt", typ: u(undefined, "") },
        { json: "label", js: "label", typ: u(undefined, "") },
        { json: "resume_picker", js: "resume_picker", typ: u(undefined, true) },
        { json: "resume_session_id", js: "resume_session_id", typ: u(undefined, "") },
        { json: "rows", js: "rows", typ: 0 },
        { json: "workspace_id", js: "workspace_id", typ: "" },
        { json: "yolo_mode", js: "yolo_mode", typ: u(undefined, true) },
    ], "any"),
    "StartReviewLoopMessage": o([
        { json: "cmd", js: "cmd", typ: r("StartReviewLoopMessageCmd") },
        { json: "handoff_payload_json", js: "handoff_payload_json", typ: u(undefined, "") },
        { json: "iteration_limit", js: "iteration_limit", typ: 0 },
        { json: "preset_id", js: "preset_id", typ: u(undefined, "") },
        { json: "prompt", js: "prompt", typ: "" },
        { json: "session_id", js: "session_id", typ: "" },
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
    "StopReviewLoopMessage": o([
        { json: "cmd", js: "cmd", typ: r("StopReviewLoopMessageCmd") },
        { json: "session_id", js: "session_id", typ: "" },
    ], "any"),
    "SubscribeGitStatusMessage": o([
        { json: "cmd", js: "cmd", typ: r("SubscribeGitStatusMessageCmd") },
        { json: "directory", js: "directory", typ: "" },
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
    "UnregisterWorkspaceMessage": o([
        { json: "cmd", js: "cmd", typ: r("UnregisterWorkspaceMessageCmd") },
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
    "UpdateEndpointMessage": o([
        { json: "cmd", js: "cmd", typ: r("UpdateEndpointMessageCmd") },
        { json: "enabled", js: "enabled", typ: u(undefined, true) },
        { json: "endpoint_id", js: "endpoint_id", typ: "" },
        { json: "name", js: "name", typ: u(undefined, "") },
        { json: "profile", js: "profile", typ: u(undefined, "") },
        { json: "ssh_target", js: "ssh_target", typ: u(undefined, "") },
    ], "any"),
    "WakeDispatchAgentMessage": o([
        { json: "cmd", js: "cmd", typ: r("WakeDispatchAgentMessageCmd") },
        { json: "dispatch_id", js: "dispatch_id", typ: "" },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
    "WakeDispatchAgentResultMessage": o([
        { json: "dispatch_id", js: "dispatch_id", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("WakeDispatchAgentResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "WebSocketEvent": o([
        { json: "action", js: "action", typ: u(undefined, "") },
        { json: "authors", js: "authors", typ: u(undefined, a(r("AuthorElement"))) },
        { json: "base_ref", js: "base_ref", typ: u(undefined, "") },
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "branches", js: "branches", typ: u(undefined, a(r("BranchElement"))) },
        { json: "chief_of_staff", js: "chief_of_staff", typ: u(undefined, true) },
        { json: "chief_of_staff_dispatch", js: "chief_of_staff_dispatch", typ: u(undefined, r("ChiefOfStaffDispatchElement")) },
        { json: "chief_of_staff_dispatches", js: "chief_of_staff_dispatches", typ: u(undefined, a(r("ChiefOfStaffDispatchElement"))) },
        { json: "cloned", js: "cloned", typ: u(undefined, true) },
        { json: "cmd", js: "cmd", typ: u(undefined, "") },
        { json: "cols", js: "cols", typ: u(undefined, 0) },
        { json: "conflict", js: "conflict", typ: u(undefined, true) },
        { json: "content", js: "content", typ: u(undefined, "") },
        { json: "data", js: "data", typ: u(undefined, "") },
        { json: "directory", js: "directory", typ: u(undefined, "") },
        { json: "dirty", js: "dirty", typ: u(undefined, true) },
        { json: "dispatch_message", js: "dispatch_message", typ: u(undefined, r("DispatchMessageObject")) },
        { json: "dispatch_messages", js: "dispatch_messages", typ: u(undefined, a(r("DispatchMessageObject"))) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: "" },
        { json: "exit_code", js: "exit_code", typ: u(undefined, 0) },
        { json: "files", js: "files", typ: u(undefined, a(r("FileElement"))) },
        { json: "found", js: "found", typ: u(undefined, true) },
        { json: "id", js: "id", typ: u(undefined, "") },
        { json: "last_seq", js: "last_seq", typ: u(undefined, 0) },
        { json: "modified", js: "modified", typ: u(undefined, "") },
        { json: "name", js: "name", typ: u(undefined, "") },
        { json: "operation", js: "operation", typ: u(undefined, r("Operation")) },
        { json: "original", js: "original", typ: u(undefined, "") },
        { json: "pane_id", js: "pane_id", typ: u(undefined, "") },
        { json: "path", js: "path", typ: u(undefined, "") },
        { json: "pid", js: "pid", typ: u(undefined, 0) },
        { json: "plugin_issues", js: "plugin_issues", typ: u(undefined, a(r("IssueElement"))) },
        { json: "plugins", js: "plugins", typ: u(undefined, a(r("PluginElement"))) },
        { json: "previous_session_id", js: "previous_session_id", typ: u(undefined, "") },
        { json: "priority", js: "priority", typ: u(undefined, 0) },
        { json: "protocol_version", js: "protocol_version", typ: u(undefined, "") },
        { json: "prs", js: "prs", typ: u(undefined, a(r("PRElement"))) },
        { json: "rate_limit_reset_at", js: "rate_limit_reset_at", typ: u(undefined, "") },
        { json: "rate_limit_resource", js: "rate_limit_resource", typ: u(undefined, "") },
        { json: "reason", js: "reason", typ: u(undefined, "") },
        { json: "recent_locations", js: "recent_locations", typ: u(undefined, a(r("RecentLocationElement"))) },
        { json: "repos", js: "repos", typ: u(undefined, a(r("RepoElement"))) },
        { json: "review_loop_run", js: "review_loop_run", typ: u(undefined, r("ReviewLoopRunObject")) },
        { json: "rows", js: "rows", typ: u(undefined, 0) },
        { json: "running", js: "running", typ: u(undefined, true) },
        { json: "runtime_id", js: "runtime_id", typ: u(undefined, "") },
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
        { json: "session_id", js: "session_id", typ: u(undefined, "") },
        { json: "sessions", js: "sessions", typ: u(undefined, a(r("SessionElement"))) },
        { json: "settings", js: "settings", typ: u(undefined, m("any")) },
        { json: "signal", js: "signal", typ: u(undefined, "") },
        { json: "split_id", js: "split_id", typ: u(undefined, "") },
        { json: "staged", js: "staged", typ: u(undefined, a(r("StagedElement"))) },
        { json: "stash_ref", js: "stash_ref", typ: u(undefined, "") },
        { json: "success", js: "success", typ: u(undefined, true) },
        { json: "target_path", js: "target_path", typ: u(undefined, "") },
        { json: "tile_id", js: "tile_id", typ: u(undefined, "") },
        { json: "tile_kind", js: "tile_kind", typ: u(undefined, "") },
        { json: "unstaged", js: "unstaged", typ: u(undefined, a(r("StagedElement"))) },
        { json: "untracked", js: "untracked", typ: u(undefined, a(r("StagedElement"))) },
        { json: "warnings", js: "warnings", typ: u(undefined, a(r("WarningElement"))) },
        { json: "workspace", js: "workspace", typ: u(undefined, r("WorkspaceElement")) },
        { json: "workspace_context_result", js: "workspace_context_result", typ: u(undefined, r("WorkspaceContextResultObject")) },
        { json: "workspace_contexts", js: "workspace_contexts", typ: u(undefined, a(r("WorkspaceContextElement"))) },
        { json: "workspace_id", js: "workspace_id", typ: u(undefined, "") },
        { json: "workspace_layout", js: "workspace_layout", typ: u(undefined, r("Layout")) },
        { json: "workspaces", js: "workspaces", typ: u(undefined, a(r("WorkspaceElement"))) },
        { json: "worktrees", js: "worktrees", typ: u(undefined, a(r("WorktreeElement"))) },
    ], "any"),
    "Workspace": o([
        { json: "directory", js: "directory", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "layout", js: "layout", typ: u(undefined, r("Layout")) },
        { json: "muted", js: "muted", typ: true },
        { json: "rank", js: "rank", typ: "" },
        { json: "status", js: "status", typ: r("WorkspaceStatus") },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "WorkspaceContext": o([
        { json: "content", js: "content", typ: "" },
        { json: "revision", js: "revision", typ: 0 },
        { json: "updated_at", js: "updated_at", typ: "" },
        { json: "updated_by_session_id", js: "updated_by_session_id", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceContextChangedMessage": o([
        { json: "event", js: "event", typ: r("WorkspaceContextChangedMessageEvent") },
        { json: "revision", js: "revision", typ: 0 },
        { json: "updated_at", js: "updated_at", typ: "" },
        { json: "updated_by_session_id", js: "updated_by_session_id", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceContextCheckoutMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceContextCheckoutMessageCmd") },
        { json: "force", js: "force", typ: u(undefined, true) },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
    "WorkspaceContextCompactMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceContextCompactMessageCmd") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
    "WorkspaceContextListMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceContextListMessageCmd") },
        { json: "request_id", js: "request_id", typ: "" },
    ], "any"),
    "WorkspaceContextListResultMessage": o([
        { json: "contexts", js: "contexts", typ: u(undefined, a(r("WorkspaceContextElement"))) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("WorkspaceContextListResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "WorkspaceContextMaintenanceResult": o([
        { json: "action", js: "action", typ: r("WorkspaceContextMaintenanceAction") },
        { json: "agent", js: "agent", typ: u(undefined, "") },
        { json: "agent_model", js: "agent_model", typ: u(undefined, "") },
        { json: "changed", js: "changed", typ: true },
        { json: "result_revision", js: "result_revision", typ: 0 },
        { json: "source_revision", js: "source_revision", typ: 0 },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceContextResult": o([
        { json: "canonical_revision", js: "canonical_revision", typ: 0 },
        { json: "modified", js: "modified", typ: true },
        { json: "path", js: "path", typ: "" },
        { json: "revision", js: "revision", typ: 0 },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "stale", js: "stale", typ: true },
        { json: "updated_at", js: "updated_at", typ: u(undefined, "") },
        { json: "updated_by_session_id", js: "updated_by_session_id", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceContextResultMessage": o([
        { json: "action", js: "action", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("WorkspaceContextResultMessageEvent") },
        { json: "result", js: "result", typ: u(undefined, r("WorkspaceContextResultObject")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "WorkspaceContextRollbackMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceContextRollbackMessageCmd") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
    "WorkspaceContextStatusMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceContextStatusMessageCmd") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
    "WorkspaceContextUpdateMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceContextUpdateMessageCmd") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
    "WorkspaceLayout": o([
        { json: "active_pane_id", js: "active_pane_id", typ: "" },
        { json: "layout_json", js: "layout_json", typ: "" },
        { json: "panes", js: "panes", typ: a(r("PaneElement")) },
        { json: "updated_at", js: "updated_at", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutActionResultMessage": o([
        { json: "action", js: "action", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("WorkspaceLayoutActionResultMessageEvent") },
        { json: "final_leaf_id", js: "final_leaf_id", typ: u(undefined, "") },
        { json: "leaf_id", js: "leaf_id", typ: u(undefined, "") },
        { json: "pane_id", js: "pane_id", typ: u(undefined, "") },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
        { json: "source_workspace_id", js: "source_workspace_id", typ: u(undefined, "") },
        { json: "split_id", js: "split_id", typ: u(undefined, "") },
        { json: "success", js: "success", typ: true },
        { json: "target_workspace_id", js: "target_workspace_id", typ: u(undefined, "") },
        { json: "tile_id", js: "tile_id", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutAddSessionPaneMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceLayoutAddSessionPaneMessageCmd") },
        { json: "direction", js: "direction", typ: u(undefined, r("WorkspaceLayoutSplitDirection")) },
        { json: "pane_id", js: "pane_id", typ: u(undefined, "") },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "target_pane_id", js: "target_pane_id", typ: u(undefined, "") },
        { json: "title", js: "title", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutClosePaneMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceLayoutClosePaneMessageCmd") },
        { json: "pane_id", js: "pane_id", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutDockTileMessage": o([
        { json: "anchor_pane_id", js: "anchor_pane_id", typ: "" },
        { json: "cmd", js: "cmd", typ: r("WorkspaceLayoutDockTileMessageCmd") },
        { json: "edge", js: "edge", typ: r("WorkspaceLayoutDockEdge") },
        { json: "ratio", js: "ratio", typ: u(undefined, 3.14) },
        { json: "tile_id", js: "tile_id", typ: "" },
        { json: "tile_kind", js: "tile_kind", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutFocusPaneMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceLayoutFocusPaneMessageCmd") },
        { json: "pane_id", js: "pane_id", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutGetMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceLayoutGetMessageCmd") },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutMessage": o([
        { json: "event", js: "event", typ: r("WorkspaceLayoutMessageEvent") },
        { json: "workspace_layout", js: "workspace_layout", typ: r("Layout") },
    ], "any"),
    "WorkspaceLayoutMoveLeafMessage": o([
        { json: "anchor_id", js: "anchor_id", typ: "" },
        { json: "cmd", js: "cmd", typ: r("WorkspaceLayoutMoveLeafMessageCmd") },
        { json: "edge", js: "edge", typ: r("WorkspaceLayoutDockEdge") },
        { json: "leaf_id", js: "leaf_id", typ: "" },
        { json: "ratio", js: "ratio", typ: u(undefined, 3.14) },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutMoveLeafToNewWorkspaceMessage": o([
        { json: "anchor_id", js: "anchor_id", typ: u(undefined, "") },
        { json: "cmd", js: "cmd", typ: r("WorkspaceLayoutMoveLeafToNewWorkspaceMessageCmd") },
        { json: "edge", js: "edge", typ: u(undefined, r("WorkspaceLayoutDockEdge")) },
        { json: "leaf_id", js: "leaf_id", typ: "" },
        { json: "ratio", js: "ratio", typ: u(undefined, 3.14) },
        { json: "source_workspace_id", js: "source_workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutMoveLeafToWorkspaceMessage": o([
        { json: "anchor_id", js: "anchor_id", typ: u(undefined, "") },
        { json: "cmd", js: "cmd", typ: r("WorkspaceLayoutMoveLeafToWorkspaceMessageCmd") },
        { json: "edge", js: "edge", typ: r("WorkspaceLayoutDockEdge") },
        { json: "leaf_id", js: "leaf_id", typ: "" },
        { json: "ratio", js: "ratio", typ: u(undefined, 3.14) },
        { json: "source_workspace_id", js: "source_workspace_id", typ: "" },
        { json: "target_workspace_id", js: "target_workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutPane": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "kind", js: "kind", typ: r("WorkspaceLayoutPaneKind") },
        { json: "pane_id", js: "pane_id", typ: "" },
        { json: "runtime_id", js: "runtime_id", typ: u(undefined, "") },
        { json: "session_id", js: "session_id", typ: u(undefined, "") },
        { json: "status", js: "status", typ: r("WorkspaceLayoutPaneStatus") },
        { json: "title", js: "title", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutRenamePaneMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceLayoutRenamePaneMessageCmd") },
        { json: "pane_id", js: "pane_id", typ: "" },
        { json: "title", js: "title", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutSetSplitRatioMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceLayoutSetSplitRatioMessageCmd") },
        { json: "ratio", js: "ratio", typ: 3.14 },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
        { json: "split_id", js: "split_id", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutUndockTileMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceLayoutUndockTileMessageCmd") },
        { json: "tile_id", js: "tile_id", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceLayoutUpdatedMessage": o([
        { json: "event", js: "event", typ: r("WorkspaceLayoutUpdatedMessageEvent") },
        { json: "workspace_layout", js: "workspace_layout", typ: r("Layout") },
    ], "any"),
    "WorkspaceLayoutUpdateTileMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceLayoutUpdateTileMessageCmd") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "tile_id", js: "tile_id", typ: "" },
        { json: "tile_params", js: "tile_params", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceRegisteredMessage": o([
        { json: "event", js: "event", typ: r("WorkspaceRegisteredMessageEvent") },
        { json: "workspace", js: "workspace", typ: r("WorkspaceElement") },
    ], "any"),
    "WorkspaceSelectedMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceSelectedMessageCmd") },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceStateChangedMessage": o([
        { json: "event", js: "event", typ: r("WorkspaceStateChangedMessageEvent") },
        { json: "workspace", js: "workspace", typ: r("WorkspaceElement") },
    ], "any"),
    "WorkspaceTileContentGetMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkspaceTileContentGetMessageCmd") },
        { json: "tile_id", js: "tile_id", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceTileContentMessage": o([
        { json: "content", js: "content", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("WorkspaceTileContentMessageEvent") },
        { json: "path", js: "path", typ: "" },
        { json: "tile_id", js: "tile_id", typ: "" },
        { json: "tile_kind", js: "tile_kind", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: "" },
    ], "any"),
    "WorkspaceUnregisteredMessage": o([
        { json: "event", js: "event", typ: r("WorkspaceUnregisteredMessageEvent") },
        { json: "workspace", js: "workspace", typ: r("WorkspaceElement") },
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
    "AcknowledgeDispatchMessageCmd": [
        "acknowledge_dispatch_message",
    ],
    "AddCommentMessageCmd": [
        "add_comment",
    ],
    "AddCommentResultMessageEvent": [
        "add_comment_result",
    ],
    "AddEndpointMessageCmd": [
        "add_endpoint",
    ],
    "AnswerReviewLoopMessageCmd": [
        "answer_review_loop",
    ],
    "ApprovePRMessageCmd": [
        "approve_pr",
    ],
    "AttachResultMessageEvent": [
        "attach_result",
    ],
    "AttachPolicy": [
        "fresh_spawn",
        "relaunch_restore",
        "same_app_remount",
    ],
    "AttachSessionMessageCmd": [
        "attach_session",
    ],
    "AuthorsUpdatedMessageEvent": [
        "authors_updated",
    ],
    "BootstrapEndpointMessageCmd": [
        "bootstrap_endpoint",
    ],
    "BranchChangedMessageEvent": [
        "branch_changed",
    ],
    "WorkspaceStatus": [
        "idle",
        "launching",
        "pending_approval",
        "scheduled",
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
    "BrowseDirectoryMessageCmd": [
        "browse_directory",
    ],
    "BrowseDirectoryResultMessageEvent": [
        "browse_directory_result",
    ],
    "BrowserControlMessageCmd": [
        "browser_control",
    ],
    "BrowserControlRequestMessageEvent": [
        "browser_control_request",
    ],
    "BrowserControlResponseMessageEvent": [
        "browser_control_response",
    ],
    "BrowserControlResultMessageCmd": [
        "browser_control_result",
    ],
    "DispatchReportType": [
        "blocker",
        "completion",
        "failure",
        "handoff",
        "progress",
    ],
    "DispatchRequestStatus": [
        "pending",
        "resolved",
    ],
    "DispatchWorkState": [
        "completed",
        "failed",
        "in_progress",
        "needs_input",
        "ready_for_review",
    ],
    "ChiefOfStaffDispatchesUpdatedMessageEvent": [
        "chief_of_staff_dispatches_updated",
    ],
    "ChiefOfStaffResultMessageEvent": [
        "chief_of_staff_result",
    ],
    "ClearSessionsMessageCmd": [
        "clear_sessions",
    ],
    "ClearWarningsMessageCmd": [
        "clear_warnings",
    ],
    "ClientHelloMessageCmd": [
        "client_hello",
    ],
    "CollapseRepoMessageCmd": [
        "collapse_repo",
    ],
    "CommandErrorMessageEvent": [
        "command_error",
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
    "DelegateMessageCmd": [
        "delegate",
    ],
    "DelegateResultMessageEvent": [
        "delegate_result",
    ],
    "DeleteCommentMessageCmd": [
        "delete_comment",
    ],
    "DeleteCommentResultMessageEvent": [
        "delete_comment_result",
    ],
    "GitOperationKind": [
        "delete_worktree",
    ],
    "DeleteWorktreeResultMessageEvent": [
        "delete_worktree_result",
    ],
    "ReasonKind": [
        "dirty_worktree",
        "git_error",
        "not_found",
        "provider_error",
    ],
    "DetachSessionMessageCmd": [
        "detach_session",
    ],
    "EndpointActionResultMessageEvent": [
        "endpoint_action_result",
    ],
    "EndpointStatusChangedMessageEvent": [
        "endpoint_status_changed",
    ],
    "EndpointsUpdatedMessageEvent": [
        "endpoints_updated",
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
    "GetDispatchMessageCmd": [
        "get_dispatch",
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
    "GetReviewLoopRunMessageCmd": [
        "get_review_loop_run",
    ],
    "GetReviewLoopStateMessageCmd": [
        "get_review_loop_state",
    ],
    "GetReviewStateMessageCmd": [
        "get_review_state",
    ],
    "GetReviewStateResultMessageEvent": [
        "get_review_state_result",
    ],
    "GetScreenSnapshotMessageCmd": [
        "get_screen_snapshot",
    ],
    "GetScreenSnapshotResultMessageEvent": [
        "get_screen_snapshot_result",
    ],
    "GetSettingsMessageCmd": [
        "get_settings",
    ],
    "GitHubHostsUpdatedMessageEvent": [
        "github_hosts_updated",
    ],
    "GitOperationStatus": [
        "failed",
        "running",
        "succeeded",
    ],
    "GitOperationFinishedMessageEvent": [
        "git_operation_finished",
    ],
    "GitOperationStartedMessageEvent": [
        "git_operation_started",
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
    "WorkspaceLayoutPaneKind": [
        "agent",
    ],
    "WorkspaceLayoutPaneStatus": [
        "failed",
        "ready",
        "spawning",
    ],
    "InjectTestPRMessageCmd": [
        "inject_test_pr",
    ],
    "InjectTestSessionMessageCmd": [
        "inject_test_session",
    ],
    "InspectPathMessageCmd": [
        "inspect_path",
    ],
    "InspectPathResultMessageEvent": [
        "inspect_path_result",
    ],
    "InstallPluginMessageCmd": [
        "install_plugin",
    ],
    "KillSessionMessageCmd": [
        "kill_session",
    ],
    "ListBranchesMessageCmd": [
        "list_branches",
    ],
    "ListDispatchesMessageCmd": [
        "list_dispatches",
    ],
    "ListDispatchMessagesMessageCmd": [
        "list_dispatch_messages",
    ],
    "ListEndpointsMessageCmd": [
        "list_endpoints",
    ],
    "ListPluginsMessageCmd": [
        "list_plugins",
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
    "MutePRMessageCmd": [
        "mute_pr",
    ],
    "MuteRepoMessageCmd": [
        "mute_repo",
    ],
    "MuteWorkspaceMessageCmd": [
        "mute_workspace",
    ],
    "OpenBrowserMessageCmd": [
        "open_browser",
    ],
    "OpenMarkdownMessageCmd": [
        "open_markdown",
    ],
    "PluginActionResultMessageEvent": [
        "plugin_action_result",
    ],
    "PluginsUpdatedMessageEvent": [
        "plugins_updated",
    ],
    "PRActionResultMessageEvent": [
        "pr_action_result",
    ],
    "PRsUpdatedMessageEvent": [
        "prs_updated",
    ],
    "PRVisitedMessageCmd": [
        "pr_visited",
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
    "PtyResizedMessageEvent": [
        "pty_resized",
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
    "ReadDispatchMessageCmd": [
        "read_dispatch_message",
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
    "RegisterWorkspaceMessageCmd": [
        "register_workspace",
    ],
    "RemoveEndpointMessageCmd": [
        "remove_endpoint",
    ],
    "RemovePluginMessageCmd": [
        "remove_plugin",
    ],
    "RenameResultMessageEvent": [
        "rename_result",
    ],
    "RenameSessionMessageCmd": [
        "rename_session",
    ],
    "RenameWorkspaceMessageCmd": [
        "rename_workspace",
    ],
    "ReportDispatchMessageCmd": [
        "report_dispatch",
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
    "ResolveDispatchRequestMessageCmd": [
        "resolve_dispatch_request",
    ],
    "ReviewLoopDecision": [
        "continue",
        "converged",
        "error",
        "needs_user_input",
    ],
    "ReviewLoopIterationStatus": [
        "awaiting_user",
        "cancelled",
        "completed",
        "error",
        "running",
    ],
    "ReviewLoopInteractionStatus": [
        "answered",
        "consumed",
        "pending",
    ],
    "ReviewLoopRunStatus": [
        "awaiting_user",
        "completed",
        "error",
        "running",
        "stopped",
    ],
    "WorkspaceContextMaintenanceAction": [
        "compact",
        "rollback",
    ],
    "ReviewLoopResultMessageEvent": [
        "review_loop_result",
    ],
    "ReviewLoopStatus": [
        "advance_received_waiting_prompt",
        "completed",
        "error",
        "running",
        "stopped",
        "waiting_for_agent_advance",
    ],
    "ReviewLoopUpdatedMessageEvent": [
        "review_loop_updated",
    ],
    "SendDispatchMessageCmd": [
        "send_dispatch_message",
    ],
    "SessionExitedMessageEvent": [
        "session_exited",
    ],
    "SessionRegisteredMessageEvent": [
        "session_registered",
    ],
    "SessionSelectedMessageCmd": [
        "session_selected",
    ],
    "SessionStateChangedMessageEvent": [
        "session_state_changed",
    ],
    "SessionsUpdatedMessageEvent": [
        "sessions_updated",
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
    "SetChiefOfStaffMessageCmd": [
        "set_chief_of_staff",
    ],
    "SetEndpointRemoteWebMessageCmd": [
        "set_endpoint_remote_web",
    ],
    "SetPluginPriorityMessageCmd": [
        "set_plugin_priority",
    ],
    "SetReviewLoopIterationLimitMessageCmd": [
        "set_review_loop_iteration_limit",
    ],
    "SetSessionResumeIDMessageCmd": [
        "set_session_resume_id",
    ],
    "SetSettingMessageCmd": [
        "set_setting",
    ],
    "SettingsUpdatedMessageEvent": [
        "settings_updated",
    ],
    "SetWorkspaceRankMessageCmd": [
        "set_workspace_rank",
    ],
    "SpawnResultMessageEvent": [
        "spawn_result",
    ],
    "SpawnSessionMessageCmd": [
        "spawn_session",
    ],
    "StartReviewLoopMessageCmd": [
        "start_review_loop",
    ],
    "StateMessageCmd": [
        "state",
    ],
    "StopMessageCmd": [
        "stop",
    ],
    "StopReviewLoopMessageCmd": [
        "stop_review_loop",
    ],
    "SubscribeGitStatusMessageCmd": [
        "subscribe_git_status",
    ],
    "TodosMessageCmd": [
        "todos",
    ],
    "UnregisterMessageCmd": [
        "unregister",
    ],
    "UnregisterWorkspaceMessageCmd": [
        "unregister_workspace",
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
    "UpdateEndpointMessageCmd": [
        "update_endpoint",
    ],
    "WakeDispatchAgentMessageCmd": [
        "wake_dispatch_agent",
    ],
    "WakeDispatchAgentResultMessageEvent": [
        "wake_dispatch_agent_result",
    ],
    "WorkspaceContextChangedMessageEvent": [
        "workspace_context_changed",
    ],
    "WorkspaceContextCheckoutMessageCmd": [
        "workspace_context_checkout",
    ],
    "WorkspaceContextCompactMessageCmd": [
        "workspace_context_compact",
    ],
    "WorkspaceContextListMessageCmd": [
        "workspace_context_list",
    ],
    "WorkspaceContextListResultMessageEvent": [
        "workspace_context_list_result",
    ],
    "WorkspaceContextResultMessageEvent": [
        "workspace_context_result",
    ],
    "WorkspaceContextRollbackMessageCmd": [
        "workspace_context_rollback",
    ],
    "WorkspaceContextStatusMessageCmd": [
        "workspace_context_status",
    ],
    "WorkspaceContextUpdateMessageCmd": [
        "workspace_context_update",
    ],
    "WorkspaceLayoutActionResultMessageEvent": [
        "workspace_layout_action_result",
    ],
    "WorkspaceLayoutAddSessionPaneMessageCmd": [
        "workspace_layout_add_session_pane",
    ],
    "WorkspaceLayoutSplitDirection": [
        "horizontal",
        "vertical",
    ],
    "WorkspaceLayoutClosePaneMessageCmd": [
        "workspace_layout_close_pane",
    ],
    "WorkspaceLayoutDockTileMessageCmd": [
        "workspace_layout_dock_tile",
    ],
    "WorkspaceLayoutDockEdge": [
        "bottom",
        "left",
        "right",
        "top",
    ],
    "WorkspaceLayoutFocusPaneMessageCmd": [
        "workspace_layout_focus_pane",
    ],
    "WorkspaceLayoutGetMessageCmd": [
        "workspace_layout_get",
    ],
    "WorkspaceLayoutMessageEvent": [
        "workspace_layout",
    ],
    "WorkspaceLayoutMoveLeafMessageCmd": [
        "workspace_layout_move_leaf",
    ],
    "WorkspaceLayoutMoveLeafToNewWorkspaceMessageCmd": [
        "workspace_layout_move_leaf_to_new_workspace",
    ],
    "WorkspaceLayoutMoveLeafToWorkspaceMessageCmd": [
        "workspace_layout_move_leaf_to_workspace",
    ],
    "WorkspaceLayoutRenamePaneMessageCmd": [
        "workspace_layout_rename_pane",
    ],
    "WorkspaceLayoutSetSplitRatioMessageCmd": [
        "workspace_layout_set_split_ratio",
    ],
    "WorkspaceLayoutUndockTileMessageCmd": [
        "workspace_layout_undock_tile",
    ],
    "WorkspaceLayoutUpdatedMessageEvent": [
        "workspace_layout_updated",
    ],
    "WorkspaceLayoutUpdateTileMessageCmd": [
        "workspace_layout_update_tile",
    ],
    "WorkspaceRegisteredMessageEvent": [
        "workspace_registered",
    ],
    "WorkspaceSelectedMessageCmd": [
        "workspace_selected",
    ],
    "WorkspaceStateChangedMessageEvent": [
        "workspace_state_changed",
    ],
    "WorkspaceTileContentGetMessageCmd": [
        "workspace_tile_content_get",
    ],
    "WorkspaceTileContentMessageEvent": [
        "workspace_tile_content",
    ],
    "WorkspaceUnregisteredMessageEvent": [
        "workspace_unregistered",
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
