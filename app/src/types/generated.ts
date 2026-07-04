// To parse this data:
//
//   import { Convert, AddCommentMessage, AddCommentResultMessage, AddEndpointMessage, ApprovePRMessage, AttachPolicy, AttachResultMessage, AttachSessionMessage, AuthorState, AuthorsUpdatedMessage, BootstrapEndpointMessage, Branch, BranchChangedMessage, BranchDiffFile, BranchDiffFilesResultMessage, BranchesResultMessage, BrowseDirectoryMessage, BrowseDirectoryResultMessage, BrowserControlMessage, BrowserControlRequestMessage, BrowserControlResponseMessage, BrowserControlResultMessage, ChiefOfStaffResultMessage, ClearSessionsMessage, ClearWarningsMessage, ClientHelloMessage, CollapseRepoMessage, CommandErrorMessage, CreateWorktreeFromBranchMessage, CreateWorktreeMessage, CreateWorktreeResultMessage, DaemonWarning, DelegateMessage, DelegateResult, DelegateResultMessage, DelegateWorktreeRequest, DeleteCommentMessage, DeleteCommentResultMessage, DeleteWorktreeMessage, DeleteWorktreeResultMessage, DetachSessionMessage, DirectoryEntry, DispatchWorkState, EndpointActionResultMessage, EndpointCapabilities, EndpointInfo, EndpointStatusChangedMessage, EndpointsUpdatedMessage, EnsureRepoMessage, EnsureRepoResultMessage, FetchPRDetailsMessage, FetchPRDetailsResultMessage, FetchRemotesMessage, FetchRemotesResultMessage, FileDiffResultMessage, FSChangedMessage, FSEntry, FSExistsMessage, FSExistsResult, FSExistsResultMessage, FSListMessage, FSListResultMessage, FSReadMessage, FSReadResult, FSReadResultMessage, FSWriteMessage, FSWriteResult, FSWriteResultMessage, GetBranchDiffFilesMessage, GetCommentsMessage, GetCommentsResultMessage, GetDefaultBranchMessage, GetDefaultBranchResultMessage, GetFileDiffMessage, GetPresentationRoundMessage, GetPresentationRoundResultMessage, GetPresentationsMessage, GetPresentationsResultMessage, GetRecentLocationsMessage, GetRepoInfoMessage, GetRepoInfoResultMessage, GetReviewStateMessage, GetReviewStateResultMessage, GetScreenSnapshotMessage, GetScreenSnapshotResultMessage, GetSettingsMessage, GetTicketMessage, GitFileChange, GitHubHostsUpdatedMessage, GitOperation, GitOperationFinishedMessage, GitOperationKind, GitOperationStartedMessage, GitOperationStatus, GitStatusUpdateMessage, HeartbeatMessage, HeatState, InitialStateMessage, InjectTestPRMessage, InjectTestSessionMessage, InspectPathMessage, InspectPathResultMessage, InstallPluginMessage, KillSessionMessage, ListBranchesMessage, ListEndpointsMessage, ListPluginsMessage, ListRemoteBranchesMessage, ListRemoteBranchesResultMessage, ListWorktreesMessage, MarkFileViewedMessage, MarkFileViewedResultMessage, MergePRMessage, MuteAuthorMessage, MutePRMessage, MuteRepoMessage, MuteWorkspaceMessage, NotebookBacklinksMessage, NotebookBacklinksResultMessage, NotebookChangedMessage, NotebookEntry, NotebookGuideMessage, NotebookGuideResult, NotebookListMessage, NotebookListResultMessage, NotebookReadMessage, NotebookReadResult, NotebookReadResultMessage, NotebookSendToChiefMessage, NotebookSendToChiefResult, NotebookSendToChiefResultMessage, NotebookWriteMessage, NotebookWriteResult, NotebookWriteResultMessage, Notification, NotificationListMessage, NotificationListResultMessage, NotificationMarkReadMessage, NotificationMarkReadResultMessage, NotificationsUpdatedMessage, OpenBrowserMessage, OpenMarkdownMessage, PathInspection, PinWorkspaceMessage, PluginActionResultMessage, PluginInfo, PluginIssue, PluginsUpdatedMessage, PR, PRActionResultMessage, Presentation, PresentationAddedMessage, PresentationComment, PresentationRound, PresentationUpdatedMessage, PresentCommentInput, PresentFeedbackMessage, PresentFeedbackResult, PresentFile, PresentManifestView, PresentOpenMessage, PresentOpenResult, PresentSubmitRoundMessage, PresentSubmitRoundResultMessage, PRRole, PRsUpdatedMessage, PRVisitedMessage, PtyDesyncMessage, PtyInputMessage, PtyOutputMessage, PtyResizedMessage, PtyResizeMessage, QueryAuthorsMessage, QueryMessage, QueryPRsMessage, QueryReposMessage, RateLimitedMessage, RecentLocation, RecentLocationsResultMessage, RefreshPRsMessage, RefreshPRsResultMessage, RegisterMessage, RegisterWorkspaceMessage, RemoveEndpointMessage, RemovePluginMessage, RenameResultMessage, RenameSessionMessage, RenameWorkspaceMessage, ReplaySegment, RepoInfo, RepoState, ReposUpdatedMessage, ResolveCommentMessage, ResolveCommentResultMessage, Response, ReviewComment, ReviewState, RuntimeRespawnedMessage, Session, SessionExitedMessage, SessionRegisteredMessage, SessionSelectedMessage, SessionState, SessionStateChangedMessage, SessionsUpdatedMessage, SessionTodosUpdatedMessage, SessionUnregisteredMessage, SessionVisualizedMessage, SetChiefOfStaffMessage, SetEndpointRemoteWebMessage, SetPluginPriorityMessage, SetSessionResumeIDMessage, SetSettingMessage, SetTicketStatusMessage, SettingsUpdatedMessage, SetWorkspaceRankMessage, SpawnResultMessage, SpawnSessionMessage, StateMessage, StopMessage, SubscribeGitStatusMessage, Task, TaskListMessage, TaskListResultMessage, TaskRetryMessage, TaskRetryResultMessage, TasksChangedMessage, Ticket, TicketActionResultMessage, TicketActivity, TicketActivityKind, TicketAddCommentMessage, TicketAttachment, TicketAttachMessage, TicketAttachResult, TicketChangeStatusMessage, TicketCommentMessage, TicketCommentResult, TicketCreateMessage, TicketCreateResult, TicketEditDescriptionMessage, TicketEvent, TicketEventBundle, TicketEventKind, TicketInboxMessage, TicketInboxResult, TicketListMessage, TicketListResult, TicketResultMessage, TicketStatus, TicketStatusResult, TicketSubscribeMessage, TicketSubscribeResult, TicketsUpdatedMessage, TicketTakeMessage, TicketTakeResult, TicketUnsubscribeMessage, TicketUnsubscribeResult, TodosMessage, TriggerNudgeMessage, UnregisterMessage, UnregisterWorkspaceMessage, UnsubscribeGitStatusMessage, UpdateCommentMessage, UpdateCommentResultMessage, UpdateEndpointMessage, WebSocketEvent, WorkflowActionResultMessage, WorkflowAgentCall, WorkflowAgentCallStatus, WorkflowCallUpsertMessage, WorkflowRun, WorkflowRunCancelMessage, WorkflowRunGetMessage, WorkflowRunListMessage, WorkflowRunStatus, WorkflowRunUpdatedMessage, WorkflowRunUpsertMessage, Workspace, WorkspaceContext, WorkspaceContextChangedMessage, WorkspaceContextCheckoutMessage, WorkspaceContextCompactMessage, WorkspaceContextListMessage, WorkspaceContextListResultMessage, WorkspaceContextMaintenanceAction, WorkspaceContextMaintenanceResult, WorkspaceContextResult, WorkspaceContextResultMessage, WorkspaceContextRollbackMessage, WorkspaceContextStatusMessage, WorkspaceContextUpdateMessage, WorkspaceLayout, WorkspaceLayoutActionResultMessage, WorkspaceLayoutAddSessionPaneMessage, WorkspaceLayoutClosePaneMessage, WorkspaceLayoutDockEdge, WorkspaceLayoutDockTileMessage, WorkspaceLayoutFocusPaneMessage, WorkspaceLayoutGetMessage, WorkspaceLayoutMessage, WorkspaceLayoutMoveLeafMessage, WorkspaceLayoutMoveLeafToNewWorkspaceMessage, WorkspaceLayoutMoveLeafToWorkspaceMessage, WorkspaceLayoutPane, WorkspaceLayoutPaneKind, WorkspaceLayoutPaneStatus, WorkspaceLayoutRenamePaneMessage, WorkspaceLayoutSetSplitRatioMessage, WorkspaceLayoutSplitDirection, WorkspaceLayoutUndockTileMessage, WorkspaceLayoutUpdatedMessage, WorkspaceLayoutUpdateTileMessage, WorkspaceRegisteredMessage, WorkspaceSelectedMessage, WorkspaceStateChangedMessage, WorkspaceStatus, WorkspaceTileContentGetMessage, WorkspaceTileContentMessage, WorkspaceUnregisteredMessage, Worktree, WorktreeCreatedEvent, WorktreeDeletedEvent, WorktreesUpdatedMessage } from "./file";
//
//   const addCommentMessage = Convert.toAddCommentMessage(json);
//   const addCommentResultMessage = Convert.toAddCommentResultMessage(json);
//   const addEndpointMessage = Convert.toAddEndpointMessage(json);
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
//   const fSChangedMessage = Convert.toFSChangedMessage(json);
//   const fSEntry = Convert.toFSEntry(json);
//   const fSExistsMessage = Convert.toFSExistsMessage(json);
//   const fSExistsResult = Convert.toFSExistsResult(json);
//   const fSExistsResultMessage = Convert.toFSExistsResultMessage(json);
//   const fSListMessage = Convert.toFSListMessage(json);
//   const fSListResultMessage = Convert.toFSListResultMessage(json);
//   const fSReadMessage = Convert.toFSReadMessage(json);
//   const fSReadResult = Convert.toFSReadResult(json);
//   const fSReadResultMessage = Convert.toFSReadResultMessage(json);
//   const fSWriteMessage = Convert.toFSWriteMessage(json);
//   const fSWriteResult = Convert.toFSWriteResult(json);
//   const fSWriteResultMessage = Convert.toFSWriteResultMessage(json);
//   const getBranchDiffFilesMessage = Convert.toGetBranchDiffFilesMessage(json);
//   const getCommentsMessage = Convert.toGetCommentsMessage(json);
//   const getCommentsResultMessage = Convert.toGetCommentsResultMessage(json);
//   const getDefaultBranchMessage = Convert.toGetDefaultBranchMessage(json);
//   const getDefaultBranchResultMessage = Convert.toGetDefaultBranchResultMessage(json);
//   const getFileDiffMessage = Convert.toGetFileDiffMessage(json);
//   const getPresentationRoundMessage = Convert.toGetPresentationRoundMessage(json);
//   const getPresentationRoundResultMessage = Convert.toGetPresentationRoundResultMessage(json);
//   const getPresentationsMessage = Convert.toGetPresentationsMessage(json);
//   const getPresentationsResultMessage = Convert.toGetPresentationsResultMessage(json);
//   const getRecentLocationsMessage = Convert.toGetRecentLocationsMessage(json);
//   const getRepoInfoMessage = Convert.toGetRepoInfoMessage(json);
//   const getRepoInfoResultMessage = Convert.toGetRepoInfoResultMessage(json);
//   const getReviewStateMessage = Convert.toGetReviewStateMessage(json);
//   const getReviewStateResultMessage = Convert.toGetReviewStateResultMessage(json);
//   const getScreenSnapshotMessage = Convert.toGetScreenSnapshotMessage(json);
//   const getScreenSnapshotResultMessage = Convert.toGetScreenSnapshotResultMessage(json);
//   const getSettingsMessage = Convert.toGetSettingsMessage(json);
//   const getTicketMessage = Convert.toGetTicketMessage(json);
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
//   const notebookBacklinksMessage = Convert.toNotebookBacklinksMessage(json);
//   const notebookBacklinksResultMessage = Convert.toNotebookBacklinksResultMessage(json);
//   const notebookChangedMessage = Convert.toNotebookChangedMessage(json);
//   const notebookEntry = Convert.toNotebookEntry(json);
//   const notebookGuideMessage = Convert.toNotebookGuideMessage(json);
//   const notebookGuideResult = Convert.toNotebookGuideResult(json);
//   const notebookListMessage = Convert.toNotebookListMessage(json);
//   const notebookListResultMessage = Convert.toNotebookListResultMessage(json);
//   const notebookReadMessage = Convert.toNotebookReadMessage(json);
//   const notebookReadResult = Convert.toNotebookReadResult(json);
//   const notebookReadResultMessage = Convert.toNotebookReadResultMessage(json);
//   const notebookSendToChiefMessage = Convert.toNotebookSendToChiefMessage(json);
//   const notebookSendToChiefResult = Convert.toNotebookSendToChiefResult(json);
//   const notebookSendToChiefResultMessage = Convert.toNotebookSendToChiefResultMessage(json);
//   const notebookWriteMessage = Convert.toNotebookWriteMessage(json);
//   const notebookWriteResult = Convert.toNotebookWriteResult(json);
//   const notebookWriteResultMessage = Convert.toNotebookWriteResultMessage(json);
//   const notification = Convert.toNotification(json);
//   const notificationListMessage = Convert.toNotificationListMessage(json);
//   const notificationListResultMessage = Convert.toNotificationListResultMessage(json);
//   const notificationMarkReadMessage = Convert.toNotificationMarkReadMessage(json);
//   const notificationMarkReadResultMessage = Convert.toNotificationMarkReadResultMessage(json);
//   const notificationsUpdatedMessage = Convert.toNotificationsUpdatedMessage(json);
//   const openBrowserMessage = Convert.toOpenBrowserMessage(json);
//   const openMarkdownMessage = Convert.toOpenMarkdownMessage(json);
//   const pathInspection = Convert.toPathInspection(json);
//   const pinWorkspaceMessage = Convert.toPinWorkspaceMessage(json);
//   const pluginActionResultMessage = Convert.toPluginActionResultMessage(json);
//   const pluginInfo = Convert.toPluginInfo(json);
//   const pluginIssue = Convert.toPluginIssue(json);
//   const pluginsUpdatedMessage = Convert.toPluginsUpdatedMessage(json);
//   const pR = Convert.toPR(json);
//   const pRActionResultMessage = Convert.toPRActionResultMessage(json);
//   const presentation = Convert.toPresentation(json);
//   const presentationAddedMessage = Convert.toPresentationAddedMessage(json);
//   const presentationComment = Convert.toPresentationComment(json);
//   const presentationRound = Convert.toPresentationRound(json);
//   const presentationUpdatedMessage = Convert.toPresentationUpdatedMessage(json);
//   const presentCommentInput = Convert.toPresentCommentInput(json);
//   const presentFeedbackMessage = Convert.toPresentFeedbackMessage(json);
//   const presentFeedbackResult = Convert.toPresentFeedbackResult(json);
//   const presentFile = Convert.toPresentFile(json);
//   const presentManifestView = Convert.toPresentManifestView(json);
//   const presentOpenMessage = Convert.toPresentOpenMessage(json);
//   const presentOpenResult = Convert.toPresentOpenResult(json);
//   const presentSubmitRoundMessage = Convert.toPresentSubmitRoundMessage(json);
//   const presentSubmitRoundResultMessage = Convert.toPresentSubmitRoundResultMessage(json);
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
//   const repoState = Convert.toRepoState(json);
//   const reposUpdatedMessage = Convert.toReposUpdatedMessage(json);
//   const resolveCommentMessage = Convert.toResolveCommentMessage(json);
//   const resolveCommentResultMessage = Convert.toResolveCommentResultMessage(json);
//   const response = Convert.toResponse(json);
//   const reviewComment = Convert.toReviewComment(json);
//   const reviewState = Convert.toReviewState(json);
//   const runtimeRespawnedMessage = Convert.toRuntimeRespawnedMessage(json);
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
//   const setSessionResumeIDMessage = Convert.toSetSessionResumeIDMessage(json);
//   const setSettingMessage = Convert.toSetSettingMessage(json);
//   const setTicketStatusMessage = Convert.toSetTicketStatusMessage(json);
//   const settingsUpdatedMessage = Convert.toSettingsUpdatedMessage(json);
//   const setWorkspaceRankMessage = Convert.toSetWorkspaceRankMessage(json);
//   const spawnResultMessage = Convert.toSpawnResultMessage(json);
//   const spawnSessionMessage = Convert.toSpawnSessionMessage(json);
//   const stateMessage = Convert.toStateMessage(json);
//   const stopMessage = Convert.toStopMessage(json);
//   const subscribeGitStatusMessage = Convert.toSubscribeGitStatusMessage(json);
//   const task = Convert.toTask(json);
//   const taskListMessage = Convert.toTaskListMessage(json);
//   const taskListResultMessage = Convert.toTaskListResultMessage(json);
//   const taskRetryMessage = Convert.toTaskRetryMessage(json);
//   const taskRetryResultMessage = Convert.toTaskRetryResultMessage(json);
//   const tasksChangedMessage = Convert.toTasksChangedMessage(json);
//   const ticket = Convert.toTicket(json);
//   const ticketActionResultMessage = Convert.toTicketActionResultMessage(json);
//   const ticketActivity = Convert.toTicketActivity(json);
//   const ticketActivityKind = Convert.toTicketActivityKind(json);
//   const ticketAddCommentMessage = Convert.toTicketAddCommentMessage(json);
//   const ticketAttachment = Convert.toTicketAttachment(json);
//   const ticketAttachMessage = Convert.toTicketAttachMessage(json);
//   const ticketAttachResult = Convert.toTicketAttachResult(json);
//   const ticketChangeStatusMessage = Convert.toTicketChangeStatusMessage(json);
//   const ticketCommentMessage = Convert.toTicketCommentMessage(json);
//   const ticketCommentResult = Convert.toTicketCommentResult(json);
//   const ticketCreateMessage = Convert.toTicketCreateMessage(json);
//   const ticketCreateResult = Convert.toTicketCreateResult(json);
//   const ticketEditDescriptionMessage = Convert.toTicketEditDescriptionMessage(json);
//   const ticketEvent = Convert.toTicketEvent(json);
//   const ticketEventBundle = Convert.toTicketEventBundle(json);
//   const ticketEventKind = Convert.toTicketEventKind(json);
//   const ticketInboxMessage = Convert.toTicketInboxMessage(json);
//   const ticketInboxResult = Convert.toTicketInboxResult(json);
//   const ticketListMessage = Convert.toTicketListMessage(json);
//   const ticketListResult = Convert.toTicketListResult(json);
//   const ticketResultMessage = Convert.toTicketResultMessage(json);
//   const ticketStatus = Convert.toTicketStatus(json);
//   const ticketStatusResult = Convert.toTicketStatusResult(json);
//   const ticketSubscribeMessage = Convert.toTicketSubscribeMessage(json);
//   const ticketSubscribeResult = Convert.toTicketSubscribeResult(json);
//   const ticketsUpdatedMessage = Convert.toTicketsUpdatedMessage(json);
//   const ticketTakeMessage = Convert.toTicketTakeMessage(json);
//   const ticketTakeResult = Convert.toTicketTakeResult(json);
//   const ticketUnsubscribeMessage = Convert.toTicketUnsubscribeMessage(json);
//   const ticketUnsubscribeResult = Convert.toTicketUnsubscribeResult(json);
//   const todosMessage = Convert.toTodosMessage(json);
//   const triggerNudgeMessage = Convert.toTriggerNudgeMessage(json);
//   const unregisterMessage = Convert.toUnregisterMessage(json);
//   const unregisterWorkspaceMessage = Convert.toUnregisterWorkspaceMessage(json);
//   const unsubscribeGitStatusMessage = Convert.toUnsubscribeGitStatusMessage(json);
//   const updateCommentMessage = Convert.toUpdateCommentMessage(json);
//   const updateCommentResultMessage = Convert.toUpdateCommentResultMessage(json);
//   const updateEndpointMessage = Convert.toUpdateEndpointMessage(json);
//   const webSocketEvent = Convert.toWebSocketEvent(json);
//   const workflowActionResultMessage = Convert.toWorkflowActionResultMessage(json);
//   const workflowAgentCall = Convert.toWorkflowAgentCall(json);
//   const workflowAgentCallStatus = Convert.toWorkflowAgentCallStatus(json);
//   const workflowCallUpsertMessage = Convert.toWorkflowCallUpsertMessage(json);
//   const workflowRun = Convert.toWorkflowRun(json);
//   const workflowRunCancelMessage = Convert.toWorkflowRunCancelMessage(json);
//   const workflowRunGetMessage = Convert.toWorkflowRunGetMessage(json);
//   const workflowRunListMessage = Convert.toWorkflowRunListMessage(json);
//   const workflowRunStatus = Convert.toWorkflowRunStatus(json);
//   const workflowRunUpdatedMessage = Convert.toWorkflowRunUpdatedMessage(json);
//   const workflowRunUpsertMessage = Convert.toWorkflowRunUpsertMessage(json);
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
    delegated_from_chief?:        boolean;
    directory:                    string;
    endpoint_id?:                 string;
    id:                           string;
    is_worktree?:                 boolean;
    label:                        string;
    last_seen:                    string;
    main_repo?:                   string;
    needs_review_after_long_run?: boolean;
    nudge_fires_at?:              string;
    recoverable?:                 boolean;
    state:                        WorkspaceStatus;
    state_since:                  string;
    state_updated_at:             string;
    ticket_unread?:               boolean;
    todos?:                       string[];
    workspace_id:                 string;
    workspace_muted?:             boolean;
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
    effort?:           string;
    label?:            string;
    model?:            string;
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

export interface FSChangedMessage {
    event:  FSChangedMessageEvent;
    origin: string;
    paths:  string[];
    [property: string]: any;
}

export enum FSChangedMessageEvent {
    FSChanged = "fs_changed",
}

export interface FSEntry {
    is_dir:    boolean;
    modified?: string;
    name:      string;
    path:      string;
    size:      number;
    [property: string]: any;
}

export interface FSExistsMessage {
    cmd:         FSExistsMessageCmd;
    path:        string;
    request_id?: string;
    [property: string]: any;
}

export enum FSExistsMessageCmd {
    FSExists = "fs_exists",
}

export interface FSExistsResult {
    exists: boolean;
    path:   string;
    [property: string]: any;
}

export interface FSExistsResultMessage {
    error?:     string;
    event:      FSExistsResultMessageEvent;
    request_id: string;
    result?:    FSExistsResultMessageResult;
    success:    boolean;
    [property: string]: any;
}

export enum FSExistsResultMessageEvent {
    FSExistsResult = "fs_exists_result",
}

export interface FSExistsResultMessageResult {
    exists: boolean;
    path:   string;
    [property: string]: any;
}

export interface FSListMessage {
    cmd:         FSListMessageCmd;
    path?:       string;
    request_id?: string;
    [property: string]: any;
}

export enum FSListMessageCmd {
    FSList = "fs_list",
}

export interface FSListResultMessage {
    entries?:   EntryObject[];
    error?:     string;
    event:      FSListResultMessageEvent;
    request_id: string;
    success:    boolean;
    [property: string]: any;
}

export interface EntryObject {
    is_dir:    boolean;
    modified?: string;
    name:      string;
    path:      string;
    size:      number;
    [property: string]: any;
}

export enum FSListResultMessageEvent {
    FSListResult = "fs_list_result",
}

export interface FSReadMessage {
    cmd:         FSReadMessageCmd;
    path:        string;
    request_id?: string;
    [property: string]: any;
}

export enum FSReadMessageCmd {
    FSRead = "fs_read",
}

export interface FSReadResult {
    content: string;
    hash:    string;
    path:    string;
    [property: string]: any;
}

export interface FSReadResultMessage {
    error?:     string;
    event:      FSReadResultMessageEvent;
    request_id: string;
    result?:    FSReadResultMessageResult;
    success:    boolean;
    [property: string]: any;
}

export enum FSReadResultMessageEvent {
    FSReadResult = "fs_read_result",
}

export interface FSReadResultMessageResult {
    content: string;
    hash:    string;
    path:    string;
    [property: string]: any;
}

export interface FSWriteMessage {
    base_hash?:  string;
    cmd:         FSWriteMessageCmd;
    content:     string;
    path:        string;
    request_id?: string;
    [property: string]: any;
}

export enum FSWriteMessageCmd {
    FSWrite = "fs_write",
}

export interface FSWriteResult {
    conflict:      boolean;
    current_hash?: string;
    hash?:         string;
    path:          string;
    [property: string]: any;
}

export interface FSWriteResultMessage {
    error?:     string;
    event:      FSWriteResultMessageEvent;
    request_id: string;
    result?:    FSWriteResultMessageResult;
    success:    boolean;
    [property: string]: any;
}

export enum FSWriteResultMessageEvent {
    FSWriteResult = "fs_write_result",
}

export interface FSWriteResultMessageResult {
    conflict:      boolean;
    current_hash?: string;
    hash?:         string;
    path:          string;
    [property: string]: any;
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

export interface GetPresentationRoundMessage {
    cmd:             GetPresentationRoundMessageCmd;
    presentation_id: string;
    seq?:            number;
    [property: string]: any;
}

export enum GetPresentationRoundMessageCmd {
    GetPresentationRound = "get_presentation_round",
}

export interface GetPresentationRoundResultMessage {
    comments?:     CommentElement[];
    error?:        string;
    event:         GetPresentationRoundResultMessageEvent;
    presentation?: PresentationElement;
    round?:        Round;
    success:       boolean;
    [property: string]: any;
}

export interface CommentElement {
    author:     string;
    content:    string;
    created_at: string;
    filepath:   string;
    id:         string;
    line_end:   number;
    line_start: number;
    round_id:   string;
    side:       string;
    [property: string]: any;
}

export enum GetPresentationRoundResultMessageEvent {
    GetPresentationRoundResult = "get_presentation_round_result",
}

export interface PresentationElement {
    created_at:             string;
    id:                     string;
    kind:                   string;
    latest_round_seq:       number;
    latest_round_submitted: boolean;
    repo_path:              string;
    session_id:             string;
    status:                 string;
    ticket_id?:             string;
    title:                  string;
    [property: string]: any;
}

export interface Round {
    base_sha:        string;
    created_at:      string;
    head_sha:        string;
    id:              string;
    manifest:        Manifest;
    presentation_id: string;
    seq:             number;
    submitted_at?:   string;
    [property: string]: any;
}

export interface Manifest {
    files:    FileObject[];
    skip:     string[];
    summary?: string;
    title:    string;
    [property: string]: any;
}

export interface FileObject {
    note?: string;
    path:  string;
    [property: string]: any;
}

export interface GetPresentationsMessage {
    cmd: GetPresentationsMessageCmd;
    [property: string]: any;
}

export enum GetPresentationsMessageCmd {
    GetPresentations = "get_presentations",
}

export interface GetPresentationsResultMessage {
    error?:        string;
    event:         GetPresentationsResultMessageEvent;
    presentations: PresentationElement[];
    success:       boolean;
    [property: string]: any;
}

export enum GetPresentationsResultMessageEvent {
    GetPresentationsResult = "get_presentations_result",
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

export interface GetTicketMessage {
    cmd:         GetTicketMessageCmd;
    request_id?: string;
    ticket_id:   string;
    [property: string]: any;
}

export enum GetTicketMessageCmd {
    GetTicket = "get_ticket",
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
    authors?:            AuthorElement[];
    daemon_instance_id?: string;
    endpoints?:          Endpoint[];
    event:               InitialStateMessageEvent;
    github_hosts?:       string[];
    protocol_version?:   string;
    prs?:                PRElement[];
    repos?:              RepoElement[];
    sessions?:           SessionElement[];
    settings?:           { [key: string]: any };
    source_fingerprint?: string;
    tickets?:            TicketElement[];
    warnings?:           WarningElement[];
    workspaces?:         WorkspaceElement[];
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

export interface TicketElement {
    activity:       ActivityElement[];
    archived_at?:   string;
    assignee:       string;
    attachments:    AttachmentElement[];
    closed_at?:     string;
    created_at:     string;
    cwd:            string;
    description:    string;
    id:             string;
    last_agent_id:  string;
    project_id:     string;
    reconciled_at?: string;
    status:         TicketStatus;
    title:          string;
    updated_at:     string;
    [property: string]: any;
}

export interface ActivityElement {
    author:       string;
    comment?:     string;
    created_at:   string;
    from_status?: TicketStatus;
    id:           number;
    kind:         TicketActivityKind;
    to_status?:   TicketStatus;
    [property: string]: any;
}

export enum TicketStatus {
    Blocked = "blocked",
    Crashed = "crashed",
    Done = "done",
    Failed = "failed",
    InReview = "in_review",
    Todo = "todo",
    Working = "working",
}

export enum TicketActivityKind {
    Comment = "comment",
    StatusChange = "status_change",
}

export interface AttachmentElement {
    created_at: string;
    filename:   string;
    id:         number;
    note?:      string;
    path:       string;
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
    pinned:    boolean;
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

export interface NotebookBacklinksMessage {
    cmd:         NotebookBacklinksMessageCmd;
    path:        string;
    request_id?: string;
    [property: string]: any;
}

export enum NotebookBacklinksMessageCmd {
    NotebookBacklinks = "notebook_backlinks",
}

export interface NotebookBacklinksResultMessage {
    entries?:   NotebookEntryElement[];
    error?:     string;
    event:      NotebookBacklinksResultMessageEvent;
    request_id: string;
    success:    boolean;
    [property: string]: any;
}

export interface NotebookEntryElement {
    path:     string;
    size:     number;
    summary?: string;
    title?:   string;
    type?:    string;
    updated?: string;
    [property: string]: any;
}

export enum NotebookBacklinksResultMessageEvent {
    NotebookBacklinksResult = "notebook_backlinks_result",
}

export interface NotebookChangedMessage {
    event:  NotebookChangedMessageEvent;
    origin: string;
    paths:  string[];
    [property: string]: any;
}

export enum NotebookChangedMessageEvent {
    NotebookChanged = "notebook_changed",
}

export interface NotebookEntry {
    path:     string;
    size:     number;
    summary?: string;
    title?:   string;
    type?:    string;
    updated?: string;
    [property: string]: any;
}

export interface NotebookGuideMessage {
    cmd:         NotebookGuideMessageCmd;
    session_id?: string;
    [property: string]: any;
}

export enum NotebookGuideMessageCmd {
    NotebookGuide = "notebook_guide",
}

export interface NotebookGuideResult {
    guidance:         string;
    root:             string;
    session_is_chief: boolean;
    [property: string]: any;
}

export interface NotebookListMessage {
    cmd:         NotebookListMessageCmd;
    prefix?:     string;
    request_id?: string;
    [property: string]: any;
}

export enum NotebookListMessageCmd {
    NotebookList = "notebook_list",
}

export interface NotebookListResultMessage {
    entries?:   NotebookEntryElement[];
    error?:     string;
    event:      NotebookListResultMessageEvent;
    request_id: string;
    success:    boolean;
    [property: string]: any;
}

export enum NotebookListResultMessageEvent {
    NotebookListResult = "notebook_list_result",
}

export interface NotebookReadMessage {
    cmd:         NotebookReadMessageCmd;
    path:        string;
    request_id?: string;
    [property: string]: any;
}

export enum NotebookReadMessageCmd {
    NotebookRead = "notebook_read",
}

export interface NotebookReadResult {
    content: string;
    hash:    string;
    path:    string;
    [property: string]: any;
}

export interface NotebookReadResultMessage {
    error?:     string;
    event:      NotebookReadResultMessageEvent;
    request_id: string;
    result?:    NotebookReadObject;
    success:    boolean;
    [property: string]: any;
}

export enum NotebookReadResultMessageEvent {
    NotebookReadResult = "notebook_read_result",
}

export interface NotebookReadObject {
    content: string;
    hash:    string;
    path:    string;
    [property: string]: any;
}

export interface NotebookSendToChiefMessage {
    cmd:          NotebookSendToChiefMessageCmd;
    request_id?:  string;
    selection:    string;
    source_path?: string;
    [property: string]: any;
}

export enum NotebookSendToChiefMessageCmd {
    NotebookSendToChief = "notebook_send_to_chief",
}

export interface NotebookSendToChiefResult {
    nudged: boolean;
    path:   string;
    [property: string]: any;
}

export interface NotebookSendToChiefResultMessage {
    error?:     string;
    event:      NotebookSendToChiefResultMessageEvent;
    request_id: string;
    result?:    NotebookSendToChiefResultMessageResult;
    success:    boolean;
    [property: string]: any;
}

export enum NotebookSendToChiefResultMessageEvent {
    NotebookSendToChiefResult = "notebook_send_to_chief_result",
}

export interface NotebookSendToChiefResultMessageResult {
    nudged: boolean;
    path:   string;
    [property: string]: any;
}

export interface NotebookWriteMessage {
    base_hash?:  string;
    cmd:         NotebookWriteMessageCmd;
    content:     string;
    path:        string;
    request_id?: string;
    [property: string]: any;
}

export enum NotebookWriteMessageCmd {
    NotebookWrite = "notebook_write",
}

export interface NotebookWriteResult {
    conflict:      boolean;
    current_hash?: string;
    hash?:         string;
    path:          string;
    [property: string]: any;
}

export interface NotebookWriteResultMessage {
    error?:     string;
    event:      NotebookWriteResultMessageEvent;
    request_id: string;
    result?:    NotebookWriteObject;
    success:    boolean;
    [property: string]: any;
}

export enum NotebookWriteResultMessageEvent {
    NotebookWriteResult = "notebook_write_result",
}

export interface NotebookWriteObject {
    conflict:      boolean;
    current_hash?: string;
    hash?:         string;
    path:          string;
    [property: string]: any;
}

export interface Notification {
    body:        string;
    created_at:  string;
    detail:      string;
    id:          string;
    kind:        string;
    read_at:     string;
    source_id:   string;
    source_kind: string;
    title:       string;
    [property: string]: any;
}

export interface NotificationListMessage {
    cmd:         NotificationListMessageCmd;
    request_id?: string;
    [property: string]: any;
}

export enum NotificationListMessageCmd {
    NotificationList = "notification_list",
}

export interface NotificationListResultMessage {
    error?:         string;
    event:          NotificationListResultMessageEvent;
    notifications?: NotificationElement[];
    request_id:     string;
    success:        boolean;
    unread_count:   number;
    [property: string]: any;
}

export enum NotificationListResultMessageEvent {
    NotificationListResult = "notification_list_result",
}

export interface NotificationElement {
    body:        string;
    created_at:  string;
    detail:      string;
    id:          string;
    kind:        string;
    read_at:     string;
    source_id:   string;
    source_kind: string;
    title:       string;
    [property: string]: any;
}

export interface NotificationMarkReadMessage {
    cmd:              NotificationMarkReadMessageCmd;
    notification_id?: string;
    request_id?:      string;
    [property: string]: any;
}

export enum NotificationMarkReadMessageCmd {
    NotificationMarkRead = "notification_mark_read",
}

export interface NotificationMarkReadResultMessage {
    error?:       string;
    event:        NotificationMarkReadResultMessageEvent;
    request_id:   string;
    success:      boolean;
    unread_count: number;
    [property: string]: any;
}

export enum NotificationMarkReadResultMessageEvent {
    NotificationMarkReadResult = "notification_mark_read_result",
}

export interface NotificationsUpdatedMessage {
    event:        NotificationsUpdatedMessageEvent;
    unread_count: number;
    [property: string]: any;
}

export enum NotificationsUpdatedMessageEvent {
    NotificationsUpdated = "notifications_updated",
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

export interface PinWorkspaceMessage {
    cmd:          PinWorkspaceMessageCmd;
    pinned:       boolean;
    workspace_id: string;
    [property: string]: any;
}

export enum PinWorkspaceMessageCmd {
    PinWorkspace = "pin_workspace",
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

export interface Presentation {
    created_at:             string;
    id:                     string;
    kind:                   string;
    latest_round_seq:       number;
    latest_round_submitted: boolean;
    repo_path:              string;
    session_id:             string;
    status:                 string;
    ticket_id?:             string;
    title:                  string;
    [property: string]: any;
}

export interface PresentationAddedMessage {
    event:        PresentationAddedMessageEvent;
    presentation: PresentationElement;
    [property: string]: any;
}

export enum PresentationAddedMessageEvent {
    PresentationAdded = "presentation_added",
}

export interface PresentationComment {
    author:     string;
    content:    string;
    created_at: string;
    filepath:   string;
    id:         string;
    line_end:   number;
    line_start: number;
    round_id:   string;
    side:       string;
    [property: string]: any;
}

export interface PresentationRound {
    base_sha:        string;
    created_at:      string;
    head_sha:        string;
    id:              string;
    manifest:        Manifest;
    presentation_id: string;
    seq:             number;
    submitted_at?:   string;
    [property: string]: any;
}

export interface PresentationUpdatedMessage {
    event:        PresentationUpdatedMessageEvent;
    presentation: PresentationElement;
    [property: string]: any;
}

export enum PresentationUpdatedMessageEvent {
    PresentationUpdated = "presentation_updated",
}

export interface PresentCommentInput {
    content:    string;
    filepath:   string;
    line_end:   number;
    line_start: number;
    side:       string;
    [property: string]: any;
}

export interface PresentFeedbackMessage {
    cmd:             PresentFeedbackMessageCmd;
    presentation_id: string;
    seq?:            number;
    [property: string]: any;
}

export enum PresentFeedbackMessageCmd {
    PresentFeedback = "present_feedback",
}

export interface PresentFeedbackResult {
    markdown:  string;
    seq:       number;
    submitted: boolean;
    [property: string]: any;
}

export interface PresentFile {
    note?: string;
    path:  string;
    [property: string]: any;
}

export interface PresentManifestView {
    files:    FileObject[];
    skip:     string[];
    summary?: string;
    title:    string;
    [property: string]: any;
}

export interface PresentOpenMessage {
    cmd:               PresentOpenMessageCmd;
    manifest_yaml:     string;
    presentation_id?:  string;
    source_session_id: string;
    ticket_id?:        string;
    [property: string]: any;
}

export enum PresentOpenMessageCmd {
    PresentOpen = "present_open",
}

export interface PresentOpenResult {
    base_sha:        string;
    head_sha:        string;
    presentation_id: string;
    round_id:        string;
    seq:             number;
    title:           string;
    [property: string]: any;
}

export interface PresentSubmitRoundMessage {
    cmd:      PresentSubmitRoundMessageCmd;
    comments: CommentObject[];
    handback: boolean;
    round_id: string;
    [property: string]: any;
}

export enum PresentSubmitRoundMessageCmd {
    PresentSubmitRound = "present_submit_round",
}

export interface CommentObject {
    content:    string;
    filepath:   string;
    line_end:   number;
    line_start: number;
    side:       string;
    [property: string]: any;
}

export interface PresentSubmitRoundResultMessage {
    error?:   string;
    event:    PresentSubmitRoundResultMessageEvent;
    round_id: string;
    success:  boolean;
    [property: string]: any;
}

export enum PresentSubmitRoundResultMessageEvent {
    PresentSubmitRoundResult = "present_submit_round_result",
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
    authors?:                              AuthorElement[];
    data?:                                 string;
    delegate_result?:                      DelegateResultObject;
    error?:                                string;
    notebook_entries?:                     NotebookEntryElement[];
    notebook_guide?:                       NotebookGuide;
    notebook_read?:                        NotebookReadObject;
    notebook_write?:                       NotebookWriteObject;
    ok:                                    boolean;
    present_feedback_result?:              PresentFeedbackResultObject;
    present_open_result?:                  PresentOpenResultObject;
    prs?:                                  PRElement[];
    repos?:                                RepoElement[];
    sessions?:                             SessionElement[];
    ticket_attach_result?:                 TicketAttachResultObject;
    ticket_comment_result?:                TicketCommentResultObject;
    ticket_create_result?:                 TicketCreateResultObject;
    ticket_inbox_result?:                  TicketInboxResultObject;
    ticket_list_result?:                   TicketListResultObject;
    ticket_status_result?:                 TicketStatusResultObject;
    ticket_subscribe_result?:              TicketSubscribeResultObject;
    ticket_take_result?:                   TicketTakeResultObject;
    ticket_unsubscribe_result?:            TicketUnsubscribeResultObject;
    workspace_context_maintenance_result?: WorkspaceContextMaintenanceResultObject;
    workspace_context_result?:             WorkspaceContextResultObject;
    workspace_contexts?:                   WorkspaceContextElement[];
    workspaces?:                           WorkspaceElement[];
    [property: string]: any;
}

export interface NotebookGuide {
    guidance:         string;
    root:             string;
    session_is_chief: boolean;
    [property: string]: any;
}

export interface PresentFeedbackResultObject {
    markdown:  string;
    seq:       number;
    submitted: boolean;
    [property: string]: any;
}

export interface PresentOpenResultObject {
    base_sha:        string;
    head_sha:        string;
    presentation_id: string;
    round_id:        string;
    seq:             number;
    title:           string;
    [property: string]: any;
}

export interface TicketAttachResultObject {
    filename:  string;
    ticket_id: string;
    [property: string]: any;
}

export interface TicketCommentResultObject {
    ticket_id: string;
    [property: string]: any;
}

export interface TicketCreateResultObject {
    status:    TicketStatus;
    ticket_id: string;
    title:     string;
    [property: string]: any;
}

export interface TicketInboxResultObject {
    bundles: BundleElement[];
    [property: string]: any;
}

export interface BundleElement {
    events:    EventElement[];
    ticket_id: string;
    [property: string]: any;
}

export interface EventElement {
    author:       string;
    comment?:     string;
    created_at:   string;
    detail?:      string;
    from_status?: TicketStatus;
    kind:         TicketEventKind;
    ticket_id:    string;
    to_status?:   TicketStatus;
    [property: string]: any;
}

export enum TicketEventKind {
    Assigned = "assigned",
    AttachmentAdded = "attachment_added",
    Commented = "commented",
    Created = "created",
    DescriptionEdited = "description_edited",
    StatusChanged = "status_changed",
}

export interface TicketListResultObject {
    tickets: TicketElement[];
    [property: string]: any;
}

export interface TicketStatusResultObject {
    status:    TicketStatus;
    ticket_id: string;
    [property: string]: any;
}

export interface TicketSubscribeResultObject {
    ticket_id: string;
    [property: string]: any;
}

export interface TicketTakeResultObject {
    previous_assignee: string;
    ticket_id:         string;
    [property: string]: any;
}

export interface TicketUnsubscribeResultObject {
    ticket_id: string;
    [property: string]: any;
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

export interface ReviewState {
    branch:       string;
    repo_path:    string;
    review_id:    string;
    viewed_files: string[];
    [property: string]: any;
}

export interface RuntimeRespawnedMessage {
    event: RuntimeRespawnedMessageEvent;
    id:    string;
    [property: string]: any;
}

export enum RuntimeRespawnedMessageEvent {
    RuntimeRespawned = "runtime_respawned",
}

export interface Session {
    agent:                        string;
    branch?:                      string;
    chief_of_staff?:              boolean;
    delegated_from_chief?:        boolean;
    directory:                    string;
    endpoint_id?:                 string;
    id:                           string;
    is_worktree?:                 boolean;
    label:                        string;
    last_seen:                    string;
    main_repo?:                   string;
    needs_review_after_long_run?: boolean;
    nudge_fires_at?:              string;
    recoverable?:                 boolean;
    state:                        WorkspaceStatus;
    state_since:                  string;
    state_updated_at:             string;
    ticket_unread?:               boolean;
    todos?:                       string[];
    workspace_id:                 string;
    workspace_muted?:             boolean;
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

export interface SetTicketStatusMessage {
    cmd:               SetTicketStatusMessageCmd;
    comment?:          string;
    source_session_id: string;
    work_state:        DispatchWorkState;
    [property: string]: any;
}

export enum SetTicketStatusMessageCmd {
    SetTicketStatus = "set_ticket_status",
}

export enum DispatchWorkState {
    Completed = "completed",
    Failed = "failed",
    InProgress = "in_progress",
    NeedsInput = "needs_input",
    ReadyForReview = "ready_for_review",
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
    chief_of_staff?:     boolean;
    claude_executable?:  string;
    cmd:                 SpawnSessionMessageCmd;
    codex_executable?:   string;
    cols:                number;
    copilot_executable?: string;
    cwd:                 string;
    effort?:             string;
    endpoint_id?:        string;
    executable?:         string;
    id:                  string;
    initial_prompt?:     string;
    label?:              string;
    model?:              string;
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

export interface Task {
    attempts:        number;
    created_at:      string;
    id:              string;
    kind:            string;
    last_error?:     string;
    next_attempt_at: string;
    state:           string;
    subject:         string;
    updated_at:      string;
    [property: string]: any;
}

export interface TaskListMessage {
    cmd:         TaskListMessageCmd;
    request_id?: string;
    [property: string]: any;
}

export enum TaskListMessageCmd {
    TaskList = "task_list",
}

export interface TaskListResultMessage {
    error?:     string;
    event:      TaskListResultMessageEvent;
    request_id: string;
    success:    boolean;
    tasks?:     TaskElement[];
    [property: string]: any;
}

export enum TaskListResultMessageEvent {
    TaskListResult = "task_list_result",
}

export interface TaskElement {
    attempts:        number;
    created_at:      string;
    id:              string;
    kind:            string;
    last_error?:     string;
    next_attempt_at: string;
    state:           string;
    subject:         string;
    updated_at:      string;
    [property: string]: any;
}

export interface TaskRetryMessage {
    cmd:         TaskRetryMessageCmd;
    request_id?: string;
    task_id:     string;
    [property: string]: any;
}

export enum TaskRetryMessageCmd {
    TaskRetry = "task_retry",
}

export interface TaskRetryResultMessage {
    error?:     string;
    event:      TaskRetryResultMessageEvent;
    request_id: string;
    success:    boolean;
    task?:      TaskElement;
    [property: string]: any;
}

export enum TaskRetryResultMessageEvent {
    TaskRetryResult = "task_retry_result",
}

export interface TasksChangedMessage {
    event: TasksChangedMessageEvent;
    [property: string]: any;
}

export enum TasksChangedMessageEvent {
    TasksChanged = "tasks_changed",
}

export interface Ticket {
    activity:       ActivityElement[];
    archived_at?:   string;
    assignee:       string;
    attachments:    AttachmentElement[];
    closed_at?:     string;
    created_at:     string;
    cwd:            string;
    description:    string;
    id:             string;
    last_agent_id:  string;
    project_id:     string;
    reconciled_at?: string;
    status:         TicketStatus;
    title:          string;
    updated_at:     string;
    [property: string]: any;
}

export interface TicketActionResultMessage {
    error?:     string;
    event:      TicketActionResultMessageEvent;
    request_id: string;
    success:    boolean;
    [property: string]: any;
}

export enum TicketActionResultMessageEvent {
    TicketActionResult = "ticket_action_result",
}

export interface TicketActivity {
    author:       string;
    comment?:     string;
    created_at:   string;
    from_status?: TicketStatus;
    id:           number;
    kind:         TicketActivityKind;
    to_status?:   TicketStatus;
    [property: string]: any;
}

export interface TicketAddCommentMessage {
    cmd:         TicketAddCommentMessageCmd;
    comment:     string;
    request_id?: string;
    ticket_id:   string;
    [property: string]: any;
}

export enum TicketAddCommentMessageCmd {
    TicketAddComment = "ticket_add_comment",
}

export interface TicketAttachment {
    created_at: string;
    filename:   string;
    id:         number;
    note?:      string;
    path:       string;
    [property: string]: any;
}

export interface TicketAttachMessage {
    cmd:               TicketAttachMessageCmd;
    filename:          string;
    note?:             string;
    source_path:       string;
    source_session_id: string;
    [property: string]: any;
}

export enum TicketAttachMessageCmd {
    TicketAttach = "ticket_attach",
}

export interface TicketAttachResult {
    filename:  string;
    ticket_id: string;
    [property: string]: any;
}

export interface TicketChangeStatusMessage {
    cmd:         TicketChangeStatusMessageCmd;
    comment?:    string;
    request_id?: string;
    status:      TicketStatus;
    ticket_id:   string;
    [property: string]: any;
}

export enum TicketChangeStatusMessageCmd {
    TicketChangeStatus = "ticket_change_status",
}

export interface TicketCommentMessage {
    cmd:               TicketCommentMessageCmd;
    comment:           string;
    source_session_id: string;
    ticket_id:         string;
    [property: string]: any;
}

export enum TicketCommentMessageCmd {
    TicketComment = "ticket_comment",
}

export interface TicketCommentResult {
    ticket_id: string;
    [property: string]: any;
}

export interface TicketCreateMessage {
    cmd:               TicketCreateMessageCmd;
    description?:      string;
    id?:               string;
    source_session_id: string;
    title:             string;
    [property: string]: any;
}

export enum TicketCreateMessageCmd {
    TicketCreate = "ticket_create",
}

export interface TicketCreateResult {
    status:    TicketStatus;
    ticket_id: string;
    title:     string;
    [property: string]: any;
}

export interface TicketEditDescriptionMessage {
    cmd:         TicketEditDescriptionMessageCmd;
    description: string;
    request_id?: string;
    ticket_id:   string;
    [property: string]: any;
}

export enum TicketEditDescriptionMessageCmd {
    TicketEditDescription = "ticket_edit_description",
}

export interface TicketEvent {
    author:       string;
    comment?:     string;
    created_at:   string;
    detail?:      string;
    from_status?: TicketStatus;
    kind:         TicketEventKind;
    ticket_id:    string;
    to_status?:   TicketStatus;
    [property: string]: any;
}

export interface TicketEventBundle {
    events:    EventElement[];
    ticket_id: string;
    [property: string]: any;
}

export interface TicketInboxMessage {
    cmd:               TicketInboxMessageCmd;
    source_session_id: string;
    [property: string]: any;
}

export enum TicketInboxMessageCmd {
    TicketInbox = "ticket_inbox",
}

export interface TicketInboxResult {
    bundles: BundleElement[];
    [property: string]: any;
}

export interface TicketListMessage {
    cmd:                TicketListMessageCmd;
    include_archived?:  boolean;
    source_session_id?: string;
    status?:            string;
    [property: string]: any;
}

export enum TicketListMessageCmd {
    TicketList = "ticket_list",
}

export interface TicketListResult {
    tickets: TicketElement[];
    [property: string]: any;
}

export interface TicketResultMessage {
    error?:     string;
    event:      TicketResultMessageEvent;
    request_id: string;
    success:    boolean;
    ticket?:    TicketElement;
    [property: string]: any;
}

export enum TicketResultMessageEvent {
    TicketResult = "ticket_result",
}

export interface TicketStatusResult {
    status:    TicketStatus;
    ticket_id: string;
    [property: string]: any;
}

export interface TicketSubscribeMessage {
    cmd:               TicketSubscribeMessageCmd;
    source_session_id: string;
    ticket_id:         string;
    [property: string]: any;
}

export enum TicketSubscribeMessageCmd {
    TicketSubscribe = "ticket_subscribe",
}

export interface TicketSubscribeResult {
    ticket_id: string;
    [property: string]: any;
}

export interface TicketsUpdatedMessage {
    event:   TicketsUpdatedMessageEvent;
    tickets: TicketElement[];
    [property: string]: any;
}

export enum TicketsUpdatedMessageEvent {
    TicketsUpdated = "tickets_updated",
}

export interface TicketTakeMessage {
    cmd:               TicketTakeMessageCmd;
    confirm?:          boolean;
    source_session_id: string;
    ticket_id:         string;
    [property: string]: any;
}

export enum TicketTakeMessageCmd {
    TicketTake = "ticket_take",
}

export interface TicketTakeResult {
    previous_assignee: string;
    ticket_id:         string;
    [property: string]: any;
}

export interface TicketUnsubscribeMessage {
    cmd:               TicketUnsubscribeMessageCmd;
    source_session_id: string;
    ticket_id:         string;
    [property: string]: any;
}

export enum TicketUnsubscribeMessageCmd {
    TicketUnsubscribe = "ticket_unsubscribe",
}

export interface TicketUnsubscribeResult {
    ticket_id: string;
    [property: string]: any;
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

export interface TriggerNudgeMessage {
    cmd:        TriggerNudgeMessageCmd;
    session_id: string;
    [property: string]: any;
}

export enum TriggerNudgeMessageCmd {
    TriggerNudge = "trigger_nudge",
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

export interface WebSocketEvent {
    action?:                   string;
    authors?:                  AuthorElement[];
    base_ref?:                 string;
    branch?:                   string;
    branches?:                 BranchElement[];
    chief_of_staff?:           boolean;
    cloned?:                   boolean;
    cmd?:                      string;
    cols?:                     number;
    conflict?:                 boolean;
    content?:                  string;
    data?:                     string;
    directory?:                string;
    dirty?:                    boolean;
    error?:                    string;
    event:                     string;
    exit_code?:                number;
    files?:                    FileElement[];
    found?:                    boolean;
    id?:                       string;
    last_seq?:                 number;
    modified?:                 string;
    name?:                     string;
    operation?:                Operation;
    original?:                 string;
    pane_id?:                  string;
    path?:                     string;
    pid?:                      number;
    plugin_issues?:            IssueElement[];
    plugins?:                  PluginElement[];
    previous_session_id?:      string;
    priority?:                 number;
    protocol_version?:         string;
    prs?:                      PRElement[];
    rate_limit_reset_at?:      string;
    rate_limit_resource?:      string;
    reason?:                   string;
    recent_locations?:         RecentLocationElement[];
    repos?:                    RepoElement[];
    rows?:                     number;
    running?:                  boolean;
    runtime_id?:               string;
    screen_cols?:              number;
    screen_cursor_visible?:    boolean;
    screen_cursor_x?:          number;
    screen_cursor_y?:          number;
    screen_rows?:              number;
    screen_snapshot?:          string;
    screen_snapshot_fresh?:    boolean;
    scrollback?:               string;
    scrollback_truncated?:     boolean;
    seq?:                      number;
    session?:                  SessionElement;
    session_id?:               string;
    sessions?:                 SessionElement[];
    settings?:                 { [key: string]: any };
    signal?:                   string;
    split_id?:                 string;
    staged?:                   StagedElement[];
    stash_ref?:                string;
    success?:                  boolean;
    target_path?:              string;
    ticket?:                   TicketElement;
    tickets?:                  TicketElement[];
    tile_id?:                  string;
    tile_kind?:                string;
    unstaged?:                 StagedElement[];
    untracked?:                StagedElement[];
    warnings?:                 WarningElement[];
    workspace?:                WorkspaceElement;
    workspace_context_result?: WorkspaceContextResultObject;
    workspace_contexts?:       WorkspaceContextElement[];
    workspace_id?:             string;
    workspace_layout?:         Layout;
    workspaces?:               WorkspaceElement[];
    worktrees?:                WorktreeElement[];
    [property: string]: any;
}

export interface WorkflowActionResultMessage {
    action:  string;
    error?:  string;
    event:   WorkflowActionResultMessageEvent;
    run?:    Run;
    run_id?: string;
    runs?:   Run[];
    success: boolean;
    [property: string]: any;
}

export enum WorkflowActionResultMessageEvent {
    WorkflowActionResult = "workflow_action_result",
}

export interface Run {
    agent_calls?:  Call[];
    args_json?:    string;
    completed_at?: string;
    created_at:    string;
    harness?:      string;
    last_error?:   string;
    phase?:        string;
    result_json?:  string;
    resumable:     boolean;
    run_id:        string;
    script_hash:   string;
    script_path:   string;
    session_id?:   string;
    status:        WorkflowRunStatus;
    updated_at:    string;
    workspace_id?: string;
    [property: string]: any;
}

export interface Call {
    agent_type?:       string;
    completed_at?:     string;
    error?:            string;
    label?:            string;
    ordinal:           string;
    phase?:            string;
    prompt_hash?:      string;
    resolved_harness?: string;
    resolved_model?:   string;
    result_json?:      string;
    result_path?:      string;
    run_id:            string;
    schema_hash?:      string;
    started_at?:       string;
    status:            WorkflowAgentCallStatus;
    [property: string]: any;
}

export enum WorkflowAgentCallStatus {
    Errored = "errored",
    Ok = "ok",
    Running = "running",
    Skipped = "skipped",
}

export enum WorkflowRunStatus {
    Canceled = "canceled",
    Completed = "completed",
    Failed = "failed",
    Running = "running",
}

export interface WorkflowAgentCall {
    agent_type?:       string;
    completed_at?:     string;
    error?:            string;
    label?:            string;
    ordinal:           string;
    phase?:            string;
    prompt_hash?:      string;
    resolved_harness?: string;
    resolved_model?:   string;
    result_json?:      string;
    result_path?:      string;
    run_id:            string;
    schema_hash?:      string;
    started_at?:       string;
    status:            WorkflowAgentCallStatus;
    [property: string]: any;
}

export interface WorkflowCallUpsertMessage {
    call:   Call;
    cmd:    WorkflowCallUpsertMessageCmd;
    run_id: string;
    [property: string]: any;
}

export enum WorkflowCallUpsertMessageCmd {
    WorkflowCallUpsert = "workflow_call_upsert",
}

export interface WorkflowRun {
    agent_calls?:  Call[];
    args_json?:    string;
    completed_at?: string;
    created_at:    string;
    harness?:      string;
    last_error?:   string;
    phase?:        string;
    result_json?:  string;
    resumable:     boolean;
    run_id:        string;
    script_hash:   string;
    script_path:   string;
    session_id?:   string;
    status:        WorkflowRunStatus;
    updated_at:    string;
    workspace_id?: string;
    [property: string]: any;
}

export interface WorkflowRunCancelMessage {
    cmd:    WorkflowRunCancelMessageCmd;
    run_id: string;
    [property: string]: any;
}

export enum WorkflowRunCancelMessageCmd {
    WorkflowRunCancel = "workflow_run_cancel",
}

export interface WorkflowRunGetMessage {
    cmd:    WorkflowRunGetMessageCmd;
    run_id: string;
    [property: string]: any;
}

export enum WorkflowRunGetMessageCmd {
    WorkflowRunGet = "workflow_run_get",
}

export interface WorkflowRunListMessage {
    cmd:           WorkflowRunListMessageCmd;
    session_id?:   string;
    workspace_id?: string;
    [property: string]: any;
}

export enum WorkflowRunListMessageCmd {
    WorkflowRunList = "workflow_run_list",
}

export interface WorkflowRunUpdatedMessage {
    event: WorkflowRunUpdatedMessageEvent;
    run:   Run;
    [property: string]: any;
}

export enum WorkflowRunUpdatedMessageEvent {
    WorkflowRunUpdated = "workflow_run_updated",
}

export interface WorkflowRunUpsertMessage {
    cmd: WorkflowRunUpsertMessageCmd;
    run: Run;
    [property: string]: any;
}

export enum WorkflowRunUpsertMessageCmd {
    WorkflowRunUpsert = "workflow_run_upsert",
}

export interface Workspace {
    directory: string;
    id:        string;
    layout?:   Layout;
    muted:     boolean;
    pinned:    boolean;
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

    public static toFSChangedMessage(json: string): FSChangedMessage {
        return cast(JSON.parse(json), r("FSChangedMessage"));
    }

    public static fSChangedMessageToJson(value: FSChangedMessage): string {
        return JSON.stringify(uncast(value, r("FSChangedMessage")), null, 2);
    }

    public static toFSEntry(json: string): FSEntry {
        return cast(JSON.parse(json), r("FSEntry"));
    }

    public static fSEntryToJson(value: FSEntry): string {
        return JSON.stringify(uncast(value, r("FSEntry")), null, 2);
    }

    public static toFSExistsMessage(json: string): FSExistsMessage {
        return cast(JSON.parse(json), r("FSExistsMessage"));
    }

    public static fSExistsMessageToJson(value: FSExistsMessage): string {
        return JSON.stringify(uncast(value, r("FSExistsMessage")), null, 2);
    }

    public static toFSExistsResult(json: string): FSExistsResult {
        return cast(JSON.parse(json), r("FSExistsResult"));
    }

    public static fSExistsResultToJson(value: FSExistsResult): string {
        return JSON.stringify(uncast(value, r("FSExistsResult")), null, 2);
    }

    public static toFSExistsResultMessage(json: string): FSExistsResultMessage {
        return cast(JSON.parse(json), r("FSExistsResultMessage"));
    }

    public static fSExistsResultMessageToJson(value: FSExistsResultMessage): string {
        return JSON.stringify(uncast(value, r("FSExistsResultMessage")), null, 2);
    }

    public static toFSListMessage(json: string): FSListMessage {
        return cast(JSON.parse(json), r("FSListMessage"));
    }

    public static fSListMessageToJson(value: FSListMessage): string {
        return JSON.stringify(uncast(value, r("FSListMessage")), null, 2);
    }

    public static toFSListResultMessage(json: string): FSListResultMessage {
        return cast(JSON.parse(json), r("FSListResultMessage"));
    }

    public static fSListResultMessageToJson(value: FSListResultMessage): string {
        return JSON.stringify(uncast(value, r("FSListResultMessage")), null, 2);
    }

    public static toFSReadMessage(json: string): FSReadMessage {
        return cast(JSON.parse(json), r("FSReadMessage"));
    }

    public static fSReadMessageToJson(value: FSReadMessage): string {
        return JSON.stringify(uncast(value, r("FSReadMessage")), null, 2);
    }

    public static toFSReadResult(json: string): FSReadResult {
        return cast(JSON.parse(json), r("FSReadResult"));
    }

    public static fSReadResultToJson(value: FSReadResult): string {
        return JSON.stringify(uncast(value, r("FSReadResult")), null, 2);
    }

    public static toFSReadResultMessage(json: string): FSReadResultMessage {
        return cast(JSON.parse(json), r("FSReadResultMessage"));
    }

    public static fSReadResultMessageToJson(value: FSReadResultMessage): string {
        return JSON.stringify(uncast(value, r("FSReadResultMessage")), null, 2);
    }

    public static toFSWriteMessage(json: string): FSWriteMessage {
        return cast(JSON.parse(json), r("FSWriteMessage"));
    }

    public static fSWriteMessageToJson(value: FSWriteMessage): string {
        return JSON.stringify(uncast(value, r("FSWriteMessage")), null, 2);
    }

    public static toFSWriteResult(json: string): FSWriteResult {
        return cast(JSON.parse(json), r("FSWriteResult"));
    }

    public static fSWriteResultToJson(value: FSWriteResult): string {
        return JSON.stringify(uncast(value, r("FSWriteResult")), null, 2);
    }

    public static toFSWriteResultMessage(json: string): FSWriteResultMessage {
        return cast(JSON.parse(json), r("FSWriteResultMessage"));
    }

    public static fSWriteResultMessageToJson(value: FSWriteResultMessage): string {
        return JSON.stringify(uncast(value, r("FSWriteResultMessage")), null, 2);
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

    public static toGetPresentationRoundMessage(json: string): GetPresentationRoundMessage {
        return cast(JSON.parse(json), r("GetPresentationRoundMessage"));
    }

    public static getPresentationRoundMessageToJson(value: GetPresentationRoundMessage): string {
        return JSON.stringify(uncast(value, r("GetPresentationRoundMessage")), null, 2);
    }

    public static toGetPresentationRoundResultMessage(json: string): GetPresentationRoundResultMessage {
        return cast(JSON.parse(json), r("GetPresentationRoundResultMessage"));
    }

    public static getPresentationRoundResultMessageToJson(value: GetPresentationRoundResultMessage): string {
        return JSON.stringify(uncast(value, r("GetPresentationRoundResultMessage")), null, 2);
    }

    public static toGetPresentationsMessage(json: string): GetPresentationsMessage {
        return cast(JSON.parse(json), r("GetPresentationsMessage"));
    }

    public static getPresentationsMessageToJson(value: GetPresentationsMessage): string {
        return JSON.stringify(uncast(value, r("GetPresentationsMessage")), null, 2);
    }

    public static toGetPresentationsResultMessage(json: string): GetPresentationsResultMessage {
        return cast(JSON.parse(json), r("GetPresentationsResultMessage"));
    }

    public static getPresentationsResultMessageToJson(value: GetPresentationsResultMessage): string {
        return JSON.stringify(uncast(value, r("GetPresentationsResultMessage")), null, 2);
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

    public static toGetTicketMessage(json: string): GetTicketMessage {
        return cast(JSON.parse(json), r("GetTicketMessage"));
    }

    public static getTicketMessageToJson(value: GetTicketMessage): string {
        return JSON.stringify(uncast(value, r("GetTicketMessage")), null, 2);
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

    public static toNotebookBacklinksMessage(json: string): NotebookBacklinksMessage {
        return cast(JSON.parse(json), r("NotebookBacklinksMessage"));
    }

    public static notebookBacklinksMessageToJson(value: NotebookBacklinksMessage): string {
        return JSON.stringify(uncast(value, r("NotebookBacklinksMessage")), null, 2);
    }

    public static toNotebookBacklinksResultMessage(json: string): NotebookBacklinksResultMessage {
        return cast(JSON.parse(json), r("NotebookBacklinksResultMessage"));
    }

    public static notebookBacklinksResultMessageToJson(value: NotebookBacklinksResultMessage): string {
        return JSON.stringify(uncast(value, r("NotebookBacklinksResultMessage")), null, 2);
    }

    public static toNotebookChangedMessage(json: string): NotebookChangedMessage {
        return cast(JSON.parse(json), r("NotebookChangedMessage"));
    }

    public static notebookChangedMessageToJson(value: NotebookChangedMessage): string {
        return JSON.stringify(uncast(value, r("NotebookChangedMessage")), null, 2);
    }

    public static toNotebookEntry(json: string): NotebookEntry {
        return cast(JSON.parse(json), r("NotebookEntry"));
    }

    public static notebookEntryToJson(value: NotebookEntry): string {
        return JSON.stringify(uncast(value, r("NotebookEntry")), null, 2);
    }

    public static toNotebookGuideMessage(json: string): NotebookGuideMessage {
        return cast(JSON.parse(json), r("NotebookGuideMessage"));
    }

    public static notebookGuideMessageToJson(value: NotebookGuideMessage): string {
        return JSON.stringify(uncast(value, r("NotebookGuideMessage")), null, 2);
    }

    public static toNotebookGuideResult(json: string): NotebookGuideResult {
        return cast(JSON.parse(json), r("NotebookGuideResult"));
    }

    public static notebookGuideResultToJson(value: NotebookGuideResult): string {
        return JSON.stringify(uncast(value, r("NotebookGuideResult")), null, 2);
    }

    public static toNotebookListMessage(json: string): NotebookListMessage {
        return cast(JSON.parse(json), r("NotebookListMessage"));
    }

    public static notebookListMessageToJson(value: NotebookListMessage): string {
        return JSON.stringify(uncast(value, r("NotebookListMessage")), null, 2);
    }

    public static toNotebookListResultMessage(json: string): NotebookListResultMessage {
        return cast(JSON.parse(json), r("NotebookListResultMessage"));
    }

    public static notebookListResultMessageToJson(value: NotebookListResultMessage): string {
        return JSON.stringify(uncast(value, r("NotebookListResultMessage")), null, 2);
    }

    public static toNotebookReadMessage(json: string): NotebookReadMessage {
        return cast(JSON.parse(json), r("NotebookReadMessage"));
    }

    public static notebookReadMessageToJson(value: NotebookReadMessage): string {
        return JSON.stringify(uncast(value, r("NotebookReadMessage")), null, 2);
    }

    public static toNotebookReadResult(json: string): NotebookReadResult {
        return cast(JSON.parse(json), r("NotebookReadResult"));
    }

    public static notebookReadResultToJson(value: NotebookReadResult): string {
        return JSON.stringify(uncast(value, r("NotebookReadResult")), null, 2);
    }

    public static toNotebookReadResultMessage(json: string): NotebookReadResultMessage {
        return cast(JSON.parse(json), r("NotebookReadResultMessage"));
    }

    public static notebookReadResultMessageToJson(value: NotebookReadResultMessage): string {
        return JSON.stringify(uncast(value, r("NotebookReadResultMessage")), null, 2);
    }

    public static toNotebookSendToChiefMessage(json: string): NotebookSendToChiefMessage {
        return cast(JSON.parse(json), r("NotebookSendToChiefMessage"));
    }

    public static notebookSendToChiefMessageToJson(value: NotebookSendToChiefMessage): string {
        return JSON.stringify(uncast(value, r("NotebookSendToChiefMessage")), null, 2);
    }

    public static toNotebookSendToChiefResult(json: string): NotebookSendToChiefResult {
        return cast(JSON.parse(json), r("NotebookSendToChiefResult"));
    }

    public static notebookSendToChiefResultToJson(value: NotebookSendToChiefResult): string {
        return JSON.stringify(uncast(value, r("NotebookSendToChiefResult")), null, 2);
    }

    public static toNotebookSendToChiefResultMessage(json: string): NotebookSendToChiefResultMessage {
        return cast(JSON.parse(json), r("NotebookSendToChiefResultMessage"));
    }

    public static notebookSendToChiefResultMessageToJson(value: NotebookSendToChiefResultMessage): string {
        return JSON.stringify(uncast(value, r("NotebookSendToChiefResultMessage")), null, 2);
    }

    public static toNotebookWriteMessage(json: string): NotebookWriteMessage {
        return cast(JSON.parse(json), r("NotebookWriteMessage"));
    }

    public static notebookWriteMessageToJson(value: NotebookWriteMessage): string {
        return JSON.stringify(uncast(value, r("NotebookWriteMessage")), null, 2);
    }

    public static toNotebookWriteResult(json: string): NotebookWriteResult {
        return cast(JSON.parse(json), r("NotebookWriteResult"));
    }

    public static notebookWriteResultToJson(value: NotebookWriteResult): string {
        return JSON.stringify(uncast(value, r("NotebookWriteResult")), null, 2);
    }

    public static toNotebookWriteResultMessage(json: string): NotebookWriteResultMessage {
        return cast(JSON.parse(json), r("NotebookWriteResultMessage"));
    }

    public static notebookWriteResultMessageToJson(value: NotebookWriteResultMessage): string {
        return JSON.stringify(uncast(value, r("NotebookWriteResultMessage")), null, 2);
    }

    public static toNotification(json: string): Notification {
        return cast(JSON.parse(json), r("Notification"));
    }

    public static notificationToJson(value: Notification): string {
        return JSON.stringify(uncast(value, r("Notification")), null, 2);
    }

    public static toNotificationListMessage(json: string): NotificationListMessage {
        return cast(JSON.parse(json), r("NotificationListMessage"));
    }

    public static notificationListMessageToJson(value: NotificationListMessage): string {
        return JSON.stringify(uncast(value, r("NotificationListMessage")), null, 2);
    }

    public static toNotificationListResultMessage(json: string): NotificationListResultMessage {
        return cast(JSON.parse(json), r("NotificationListResultMessage"));
    }

    public static notificationListResultMessageToJson(value: NotificationListResultMessage): string {
        return JSON.stringify(uncast(value, r("NotificationListResultMessage")), null, 2);
    }

    public static toNotificationMarkReadMessage(json: string): NotificationMarkReadMessage {
        return cast(JSON.parse(json), r("NotificationMarkReadMessage"));
    }

    public static notificationMarkReadMessageToJson(value: NotificationMarkReadMessage): string {
        return JSON.stringify(uncast(value, r("NotificationMarkReadMessage")), null, 2);
    }

    public static toNotificationMarkReadResultMessage(json: string): NotificationMarkReadResultMessage {
        return cast(JSON.parse(json), r("NotificationMarkReadResultMessage"));
    }

    public static notificationMarkReadResultMessageToJson(value: NotificationMarkReadResultMessage): string {
        return JSON.stringify(uncast(value, r("NotificationMarkReadResultMessage")), null, 2);
    }

    public static toNotificationsUpdatedMessage(json: string): NotificationsUpdatedMessage {
        return cast(JSON.parse(json), r("NotificationsUpdatedMessage"));
    }

    public static notificationsUpdatedMessageToJson(value: NotificationsUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("NotificationsUpdatedMessage")), null, 2);
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

    public static toPinWorkspaceMessage(json: string): PinWorkspaceMessage {
        return cast(JSON.parse(json), r("PinWorkspaceMessage"));
    }

    public static pinWorkspaceMessageToJson(value: PinWorkspaceMessage): string {
        return JSON.stringify(uncast(value, r("PinWorkspaceMessage")), null, 2);
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

    public static toPresentation(json: string): Presentation {
        return cast(JSON.parse(json), r("Presentation"));
    }

    public static presentationToJson(value: Presentation): string {
        return JSON.stringify(uncast(value, r("Presentation")), null, 2);
    }

    public static toPresentationAddedMessage(json: string): PresentationAddedMessage {
        return cast(JSON.parse(json), r("PresentationAddedMessage"));
    }

    public static presentationAddedMessageToJson(value: PresentationAddedMessage): string {
        return JSON.stringify(uncast(value, r("PresentationAddedMessage")), null, 2);
    }

    public static toPresentationComment(json: string): PresentationComment {
        return cast(JSON.parse(json), r("PresentationComment"));
    }

    public static presentationCommentToJson(value: PresentationComment): string {
        return JSON.stringify(uncast(value, r("PresentationComment")), null, 2);
    }

    public static toPresentationRound(json: string): PresentationRound {
        return cast(JSON.parse(json), r("PresentationRound"));
    }

    public static presentationRoundToJson(value: PresentationRound): string {
        return JSON.stringify(uncast(value, r("PresentationRound")), null, 2);
    }

    public static toPresentationUpdatedMessage(json: string): PresentationUpdatedMessage {
        return cast(JSON.parse(json), r("PresentationUpdatedMessage"));
    }

    public static presentationUpdatedMessageToJson(value: PresentationUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("PresentationUpdatedMessage")), null, 2);
    }

    public static toPresentCommentInput(json: string): PresentCommentInput {
        return cast(JSON.parse(json), r("PresentCommentInput"));
    }

    public static presentCommentInputToJson(value: PresentCommentInput): string {
        return JSON.stringify(uncast(value, r("PresentCommentInput")), null, 2);
    }

    public static toPresentFeedbackMessage(json: string): PresentFeedbackMessage {
        return cast(JSON.parse(json), r("PresentFeedbackMessage"));
    }

    public static presentFeedbackMessageToJson(value: PresentFeedbackMessage): string {
        return JSON.stringify(uncast(value, r("PresentFeedbackMessage")), null, 2);
    }

    public static toPresentFeedbackResult(json: string): PresentFeedbackResult {
        return cast(JSON.parse(json), r("PresentFeedbackResult"));
    }

    public static presentFeedbackResultToJson(value: PresentFeedbackResult): string {
        return JSON.stringify(uncast(value, r("PresentFeedbackResult")), null, 2);
    }

    public static toPresentFile(json: string): PresentFile {
        return cast(JSON.parse(json), r("PresentFile"));
    }

    public static presentFileToJson(value: PresentFile): string {
        return JSON.stringify(uncast(value, r("PresentFile")), null, 2);
    }

    public static toPresentManifestView(json: string): PresentManifestView {
        return cast(JSON.parse(json), r("PresentManifestView"));
    }

    public static presentManifestViewToJson(value: PresentManifestView): string {
        return JSON.stringify(uncast(value, r("PresentManifestView")), null, 2);
    }

    public static toPresentOpenMessage(json: string): PresentOpenMessage {
        return cast(JSON.parse(json), r("PresentOpenMessage"));
    }

    public static presentOpenMessageToJson(value: PresentOpenMessage): string {
        return JSON.stringify(uncast(value, r("PresentOpenMessage")), null, 2);
    }

    public static toPresentOpenResult(json: string): PresentOpenResult {
        return cast(JSON.parse(json), r("PresentOpenResult"));
    }

    public static presentOpenResultToJson(value: PresentOpenResult): string {
        return JSON.stringify(uncast(value, r("PresentOpenResult")), null, 2);
    }

    public static toPresentSubmitRoundMessage(json: string): PresentSubmitRoundMessage {
        return cast(JSON.parse(json), r("PresentSubmitRoundMessage"));
    }

    public static presentSubmitRoundMessageToJson(value: PresentSubmitRoundMessage): string {
        return JSON.stringify(uncast(value, r("PresentSubmitRoundMessage")), null, 2);
    }

    public static toPresentSubmitRoundResultMessage(json: string): PresentSubmitRoundResultMessage {
        return cast(JSON.parse(json), r("PresentSubmitRoundResultMessage"));
    }

    public static presentSubmitRoundResultMessageToJson(value: PresentSubmitRoundResultMessage): string {
        return JSON.stringify(uncast(value, r("PresentSubmitRoundResultMessage")), null, 2);
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

    public static toReviewComment(json: string): ReviewComment {
        return cast(JSON.parse(json), r("ReviewComment"));
    }

    public static reviewCommentToJson(value: ReviewComment): string {
        return JSON.stringify(uncast(value, r("ReviewComment")), null, 2);
    }

    public static toReviewState(json: string): ReviewState {
        return cast(JSON.parse(json), r("ReviewState"));
    }

    public static reviewStateToJson(value: ReviewState): string {
        return JSON.stringify(uncast(value, r("ReviewState")), null, 2);
    }

    public static toRuntimeRespawnedMessage(json: string): RuntimeRespawnedMessage {
        return cast(JSON.parse(json), r("RuntimeRespawnedMessage"));
    }

    public static runtimeRespawnedMessageToJson(value: RuntimeRespawnedMessage): string {
        return JSON.stringify(uncast(value, r("RuntimeRespawnedMessage")), null, 2);
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

    public static toSetTicketStatusMessage(json: string): SetTicketStatusMessage {
        return cast(JSON.parse(json), r("SetTicketStatusMessage"));
    }

    public static setTicketStatusMessageToJson(value: SetTicketStatusMessage): string {
        return JSON.stringify(uncast(value, r("SetTicketStatusMessage")), null, 2);
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

    public static toTask(json: string): Task {
        return cast(JSON.parse(json), r("Task"));
    }

    public static taskToJson(value: Task): string {
        return JSON.stringify(uncast(value, r("Task")), null, 2);
    }

    public static toTaskListMessage(json: string): TaskListMessage {
        return cast(JSON.parse(json), r("TaskListMessage"));
    }

    public static taskListMessageToJson(value: TaskListMessage): string {
        return JSON.stringify(uncast(value, r("TaskListMessage")), null, 2);
    }

    public static toTaskListResultMessage(json: string): TaskListResultMessage {
        return cast(JSON.parse(json), r("TaskListResultMessage"));
    }

    public static taskListResultMessageToJson(value: TaskListResultMessage): string {
        return JSON.stringify(uncast(value, r("TaskListResultMessage")), null, 2);
    }

    public static toTaskRetryMessage(json: string): TaskRetryMessage {
        return cast(JSON.parse(json), r("TaskRetryMessage"));
    }

    public static taskRetryMessageToJson(value: TaskRetryMessage): string {
        return JSON.stringify(uncast(value, r("TaskRetryMessage")), null, 2);
    }

    public static toTaskRetryResultMessage(json: string): TaskRetryResultMessage {
        return cast(JSON.parse(json), r("TaskRetryResultMessage"));
    }

    public static taskRetryResultMessageToJson(value: TaskRetryResultMessage): string {
        return JSON.stringify(uncast(value, r("TaskRetryResultMessage")), null, 2);
    }

    public static toTasksChangedMessage(json: string): TasksChangedMessage {
        return cast(JSON.parse(json), r("TasksChangedMessage"));
    }

    public static tasksChangedMessageToJson(value: TasksChangedMessage): string {
        return JSON.stringify(uncast(value, r("TasksChangedMessage")), null, 2);
    }

    public static toTicket(json: string): Ticket {
        return cast(JSON.parse(json), r("Ticket"));
    }

    public static ticketToJson(value: Ticket): string {
        return JSON.stringify(uncast(value, r("Ticket")), null, 2);
    }

    public static toTicketActionResultMessage(json: string): TicketActionResultMessage {
        return cast(JSON.parse(json), r("TicketActionResultMessage"));
    }

    public static ticketActionResultMessageToJson(value: TicketActionResultMessage): string {
        return JSON.stringify(uncast(value, r("TicketActionResultMessage")), null, 2);
    }

    public static toTicketActivity(json: string): TicketActivity {
        return cast(JSON.parse(json), r("TicketActivity"));
    }

    public static ticketActivityToJson(value: TicketActivity): string {
        return JSON.stringify(uncast(value, r("TicketActivity")), null, 2);
    }

    public static toTicketActivityKind(json: string): TicketActivityKind {
        return cast(JSON.parse(json), r("TicketActivityKind"));
    }

    public static ticketActivityKindToJson(value: TicketActivityKind): string {
        return JSON.stringify(uncast(value, r("TicketActivityKind")), null, 2);
    }

    public static toTicketAddCommentMessage(json: string): TicketAddCommentMessage {
        return cast(JSON.parse(json), r("TicketAddCommentMessage"));
    }

    public static ticketAddCommentMessageToJson(value: TicketAddCommentMessage): string {
        return JSON.stringify(uncast(value, r("TicketAddCommentMessage")), null, 2);
    }

    public static toTicketAttachment(json: string): TicketAttachment {
        return cast(JSON.parse(json), r("TicketAttachment"));
    }

    public static ticketAttachmentToJson(value: TicketAttachment): string {
        return JSON.stringify(uncast(value, r("TicketAttachment")), null, 2);
    }

    public static toTicketAttachMessage(json: string): TicketAttachMessage {
        return cast(JSON.parse(json), r("TicketAttachMessage"));
    }

    public static ticketAttachMessageToJson(value: TicketAttachMessage): string {
        return JSON.stringify(uncast(value, r("TicketAttachMessage")), null, 2);
    }

    public static toTicketAttachResult(json: string): TicketAttachResult {
        return cast(JSON.parse(json), r("TicketAttachResult"));
    }

    public static ticketAttachResultToJson(value: TicketAttachResult): string {
        return JSON.stringify(uncast(value, r("TicketAttachResult")), null, 2);
    }

    public static toTicketChangeStatusMessage(json: string): TicketChangeStatusMessage {
        return cast(JSON.parse(json), r("TicketChangeStatusMessage"));
    }

    public static ticketChangeStatusMessageToJson(value: TicketChangeStatusMessage): string {
        return JSON.stringify(uncast(value, r("TicketChangeStatusMessage")), null, 2);
    }

    public static toTicketCommentMessage(json: string): TicketCommentMessage {
        return cast(JSON.parse(json), r("TicketCommentMessage"));
    }

    public static ticketCommentMessageToJson(value: TicketCommentMessage): string {
        return JSON.stringify(uncast(value, r("TicketCommentMessage")), null, 2);
    }

    public static toTicketCommentResult(json: string): TicketCommentResult {
        return cast(JSON.parse(json), r("TicketCommentResult"));
    }

    public static ticketCommentResultToJson(value: TicketCommentResult): string {
        return JSON.stringify(uncast(value, r("TicketCommentResult")), null, 2);
    }

    public static toTicketCreateMessage(json: string): TicketCreateMessage {
        return cast(JSON.parse(json), r("TicketCreateMessage"));
    }

    public static ticketCreateMessageToJson(value: TicketCreateMessage): string {
        return JSON.stringify(uncast(value, r("TicketCreateMessage")), null, 2);
    }

    public static toTicketCreateResult(json: string): TicketCreateResult {
        return cast(JSON.parse(json), r("TicketCreateResult"));
    }

    public static ticketCreateResultToJson(value: TicketCreateResult): string {
        return JSON.stringify(uncast(value, r("TicketCreateResult")), null, 2);
    }

    public static toTicketEditDescriptionMessage(json: string): TicketEditDescriptionMessage {
        return cast(JSON.parse(json), r("TicketEditDescriptionMessage"));
    }

    public static ticketEditDescriptionMessageToJson(value: TicketEditDescriptionMessage): string {
        return JSON.stringify(uncast(value, r("TicketEditDescriptionMessage")), null, 2);
    }

    public static toTicketEvent(json: string): TicketEvent {
        return cast(JSON.parse(json), r("TicketEvent"));
    }

    public static ticketEventToJson(value: TicketEvent): string {
        return JSON.stringify(uncast(value, r("TicketEvent")), null, 2);
    }

    public static toTicketEventBundle(json: string): TicketEventBundle {
        return cast(JSON.parse(json), r("TicketEventBundle"));
    }

    public static ticketEventBundleToJson(value: TicketEventBundle): string {
        return JSON.stringify(uncast(value, r("TicketEventBundle")), null, 2);
    }

    public static toTicketEventKind(json: string): TicketEventKind {
        return cast(JSON.parse(json), r("TicketEventKind"));
    }

    public static ticketEventKindToJson(value: TicketEventKind): string {
        return JSON.stringify(uncast(value, r("TicketEventKind")), null, 2);
    }

    public static toTicketInboxMessage(json: string): TicketInboxMessage {
        return cast(JSON.parse(json), r("TicketInboxMessage"));
    }

    public static ticketInboxMessageToJson(value: TicketInboxMessage): string {
        return JSON.stringify(uncast(value, r("TicketInboxMessage")), null, 2);
    }

    public static toTicketInboxResult(json: string): TicketInboxResult {
        return cast(JSON.parse(json), r("TicketInboxResult"));
    }

    public static ticketInboxResultToJson(value: TicketInboxResult): string {
        return JSON.stringify(uncast(value, r("TicketInboxResult")), null, 2);
    }

    public static toTicketListMessage(json: string): TicketListMessage {
        return cast(JSON.parse(json), r("TicketListMessage"));
    }

    public static ticketListMessageToJson(value: TicketListMessage): string {
        return JSON.stringify(uncast(value, r("TicketListMessage")), null, 2);
    }

    public static toTicketListResult(json: string): TicketListResult {
        return cast(JSON.parse(json), r("TicketListResult"));
    }

    public static ticketListResultToJson(value: TicketListResult): string {
        return JSON.stringify(uncast(value, r("TicketListResult")), null, 2);
    }

    public static toTicketResultMessage(json: string): TicketResultMessage {
        return cast(JSON.parse(json), r("TicketResultMessage"));
    }

    public static ticketResultMessageToJson(value: TicketResultMessage): string {
        return JSON.stringify(uncast(value, r("TicketResultMessage")), null, 2);
    }

    public static toTicketStatus(json: string): TicketStatus {
        return cast(JSON.parse(json), r("TicketStatus"));
    }

    public static ticketStatusToJson(value: TicketStatus): string {
        return JSON.stringify(uncast(value, r("TicketStatus")), null, 2);
    }

    public static toTicketStatusResult(json: string): TicketStatusResult {
        return cast(JSON.parse(json), r("TicketStatusResult"));
    }

    public static ticketStatusResultToJson(value: TicketStatusResult): string {
        return JSON.stringify(uncast(value, r("TicketStatusResult")), null, 2);
    }

    public static toTicketSubscribeMessage(json: string): TicketSubscribeMessage {
        return cast(JSON.parse(json), r("TicketSubscribeMessage"));
    }

    public static ticketSubscribeMessageToJson(value: TicketSubscribeMessage): string {
        return JSON.stringify(uncast(value, r("TicketSubscribeMessage")), null, 2);
    }

    public static toTicketSubscribeResult(json: string): TicketSubscribeResult {
        return cast(JSON.parse(json), r("TicketSubscribeResult"));
    }

    public static ticketSubscribeResultToJson(value: TicketSubscribeResult): string {
        return JSON.stringify(uncast(value, r("TicketSubscribeResult")), null, 2);
    }

    public static toTicketsUpdatedMessage(json: string): TicketsUpdatedMessage {
        return cast(JSON.parse(json), r("TicketsUpdatedMessage"));
    }

    public static ticketsUpdatedMessageToJson(value: TicketsUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("TicketsUpdatedMessage")), null, 2);
    }

    public static toTicketTakeMessage(json: string): TicketTakeMessage {
        return cast(JSON.parse(json), r("TicketTakeMessage"));
    }

    public static ticketTakeMessageToJson(value: TicketTakeMessage): string {
        return JSON.stringify(uncast(value, r("TicketTakeMessage")), null, 2);
    }

    public static toTicketTakeResult(json: string): TicketTakeResult {
        return cast(JSON.parse(json), r("TicketTakeResult"));
    }

    public static ticketTakeResultToJson(value: TicketTakeResult): string {
        return JSON.stringify(uncast(value, r("TicketTakeResult")), null, 2);
    }

    public static toTicketUnsubscribeMessage(json: string): TicketUnsubscribeMessage {
        return cast(JSON.parse(json), r("TicketUnsubscribeMessage"));
    }

    public static ticketUnsubscribeMessageToJson(value: TicketUnsubscribeMessage): string {
        return JSON.stringify(uncast(value, r("TicketUnsubscribeMessage")), null, 2);
    }

    public static toTicketUnsubscribeResult(json: string): TicketUnsubscribeResult {
        return cast(JSON.parse(json), r("TicketUnsubscribeResult"));
    }

    public static ticketUnsubscribeResultToJson(value: TicketUnsubscribeResult): string {
        return JSON.stringify(uncast(value, r("TicketUnsubscribeResult")), null, 2);
    }

    public static toTodosMessage(json: string): TodosMessage {
        return cast(JSON.parse(json), r("TodosMessage"));
    }

    public static todosMessageToJson(value: TodosMessage): string {
        return JSON.stringify(uncast(value, r("TodosMessage")), null, 2);
    }

    public static toTriggerNudgeMessage(json: string): TriggerNudgeMessage {
        return cast(JSON.parse(json), r("TriggerNudgeMessage"));
    }

    public static triggerNudgeMessageToJson(value: TriggerNudgeMessage): string {
        return JSON.stringify(uncast(value, r("TriggerNudgeMessage")), null, 2);
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

    public static toWebSocketEvent(json: string): WebSocketEvent {
        return cast(JSON.parse(json), r("WebSocketEvent"));
    }

    public static webSocketEventToJson(value: WebSocketEvent): string {
        return JSON.stringify(uncast(value, r("WebSocketEvent")), null, 2);
    }

    public static toWorkflowActionResultMessage(json: string): WorkflowActionResultMessage {
        return cast(JSON.parse(json), r("WorkflowActionResultMessage"));
    }

    public static workflowActionResultMessageToJson(value: WorkflowActionResultMessage): string {
        return JSON.stringify(uncast(value, r("WorkflowActionResultMessage")), null, 2);
    }

    public static toWorkflowAgentCall(json: string): WorkflowAgentCall {
        return cast(JSON.parse(json), r("WorkflowAgentCall"));
    }

    public static workflowAgentCallToJson(value: WorkflowAgentCall): string {
        return JSON.stringify(uncast(value, r("WorkflowAgentCall")), null, 2);
    }

    public static toWorkflowAgentCallStatus(json: string): WorkflowAgentCallStatus {
        return cast(JSON.parse(json), r("WorkflowAgentCallStatus"));
    }

    public static workflowAgentCallStatusToJson(value: WorkflowAgentCallStatus): string {
        return JSON.stringify(uncast(value, r("WorkflowAgentCallStatus")), null, 2);
    }

    public static toWorkflowCallUpsertMessage(json: string): WorkflowCallUpsertMessage {
        return cast(JSON.parse(json), r("WorkflowCallUpsertMessage"));
    }

    public static workflowCallUpsertMessageToJson(value: WorkflowCallUpsertMessage): string {
        return JSON.stringify(uncast(value, r("WorkflowCallUpsertMessage")), null, 2);
    }

    public static toWorkflowRun(json: string): WorkflowRun {
        return cast(JSON.parse(json), r("WorkflowRun"));
    }

    public static workflowRunToJson(value: WorkflowRun): string {
        return JSON.stringify(uncast(value, r("WorkflowRun")), null, 2);
    }

    public static toWorkflowRunCancelMessage(json: string): WorkflowRunCancelMessage {
        return cast(JSON.parse(json), r("WorkflowRunCancelMessage"));
    }

    public static workflowRunCancelMessageToJson(value: WorkflowRunCancelMessage): string {
        return JSON.stringify(uncast(value, r("WorkflowRunCancelMessage")), null, 2);
    }

    public static toWorkflowRunGetMessage(json: string): WorkflowRunGetMessage {
        return cast(JSON.parse(json), r("WorkflowRunGetMessage"));
    }

    public static workflowRunGetMessageToJson(value: WorkflowRunGetMessage): string {
        return JSON.stringify(uncast(value, r("WorkflowRunGetMessage")), null, 2);
    }

    public static toWorkflowRunListMessage(json: string): WorkflowRunListMessage {
        return cast(JSON.parse(json), r("WorkflowRunListMessage"));
    }

    public static workflowRunListMessageToJson(value: WorkflowRunListMessage): string {
        return JSON.stringify(uncast(value, r("WorkflowRunListMessage")), null, 2);
    }

    public static toWorkflowRunStatus(json: string): WorkflowRunStatus {
        return cast(JSON.parse(json), r("WorkflowRunStatus"));
    }

    public static workflowRunStatusToJson(value: WorkflowRunStatus): string {
        return JSON.stringify(uncast(value, r("WorkflowRunStatus")), null, 2);
    }

    public static toWorkflowRunUpdatedMessage(json: string): WorkflowRunUpdatedMessage {
        return cast(JSON.parse(json), r("WorkflowRunUpdatedMessage"));
    }

    public static workflowRunUpdatedMessageToJson(value: WorkflowRunUpdatedMessage): string {
        return JSON.stringify(uncast(value, r("WorkflowRunUpdatedMessage")), null, 2);
    }

    public static toWorkflowRunUpsertMessage(json: string): WorkflowRunUpsertMessage {
        return cast(JSON.parse(json), r("WorkflowRunUpsertMessage"));
    }

    public static workflowRunUpsertMessageToJson(value: WorkflowRunUpsertMessage): string {
        return JSON.stringify(uncast(value, r("WorkflowRunUpsertMessage")), null, 2);
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
        { json: "delegated_from_chief", js: "delegated_from_chief", typ: u(undefined, true) },
        { json: "directory", js: "directory", typ: "" },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "is_worktree", js: "is_worktree", typ: u(undefined, true) },
        { json: "label", js: "label", typ: "" },
        { json: "last_seen", js: "last_seen", typ: "" },
        { json: "main_repo", js: "main_repo", typ: u(undefined, "") },
        { json: "needs_review_after_long_run", js: "needs_review_after_long_run", typ: u(undefined, true) },
        { json: "nudge_fires_at", js: "nudge_fires_at", typ: u(undefined, "") },
        { json: "recoverable", js: "recoverable", typ: u(undefined, true) },
        { json: "state", js: "state", typ: r("WorkspaceStatus") },
        { json: "state_since", js: "state_since", typ: "" },
        { json: "state_updated_at", js: "state_updated_at", typ: "" },
        { json: "ticket_unread", js: "ticket_unread", typ: u(undefined, true) },
        { json: "todos", js: "todos", typ: u(undefined, a("")) },
        { json: "workspace_id", js: "workspace_id", typ: "" },
        { json: "workspace_muted", js: "workspace_muted", typ: u(undefined, true) },
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
        { json: "effort", js: "effort", typ: u(undefined, "") },
        { json: "label", js: "label", typ: u(undefined, "") },
        { json: "model", js: "model", typ: u(undefined, "") },
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
    "FSChangedMessage": o([
        { json: "event", js: "event", typ: r("FSChangedMessageEvent") },
        { json: "origin", js: "origin", typ: "" },
        { json: "paths", js: "paths", typ: a("") },
    ], "any"),
    "FSEntry": o([
        { json: "is_dir", js: "is_dir", typ: true },
        { json: "modified", js: "modified", typ: u(undefined, "") },
        { json: "name", js: "name", typ: "" },
        { json: "path", js: "path", typ: "" },
        { json: "size", js: "size", typ: 0 },
    ], "any"),
    "FSExistsMessage": o([
        { json: "cmd", js: "cmd", typ: r("FSExistsMessageCmd") },
        { json: "path", js: "path", typ: "" },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "FSExistsResult": o([
        { json: "exists", js: "exists", typ: true },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "FSExistsResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("FSExistsResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "result", js: "result", typ: u(undefined, r("FSExistsResultMessageResult")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "FSExistsResultMessageResult": o([
        { json: "exists", js: "exists", typ: true },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "FSListMessage": o([
        { json: "cmd", js: "cmd", typ: r("FSListMessageCmd") },
        { json: "path", js: "path", typ: u(undefined, "") },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "FSListResultMessage": o([
        { json: "entries", js: "entries", typ: u(undefined, a(r("EntryObject"))) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("FSListResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "EntryObject": o([
        { json: "is_dir", js: "is_dir", typ: true },
        { json: "modified", js: "modified", typ: u(undefined, "") },
        { json: "name", js: "name", typ: "" },
        { json: "path", js: "path", typ: "" },
        { json: "size", js: "size", typ: 0 },
    ], "any"),
    "FSReadMessage": o([
        { json: "cmd", js: "cmd", typ: r("FSReadMessageCmd") },
        { json: "path", js: "path", typ: "" },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "FSReadResult": o([
        { json: "content", js: "content", typ: "" },
        { json: "hash", js: "hash", typ: "" },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "FSReadResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("FSReadResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "result", js: "result", typ: u(undefined, r("FSReadResultMessageResult")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "FSReadResultMessageResult": o([
        { json: "content", js: "content", typ: "" },
        { json: "hash", js: "hash", typ: "" },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "FSWriteMessage": o([
        { json: "base_hash", js: "base_hash", typ: u(undefined, "") },
        { json: "cmd", js: "cmd", typ: r("FSWriteMessageCmd") },
        { json: "content", js: "content", typ: "" },
        { json: "path", js: "path", typ: "" },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "FSWriteResult": o([
        { json: "conflict", js: "conflict", typ: true },
        { json: "current_hash", js: "current_hash", typ: u(undefined, "") },
        { json: "hash", js: "hash", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "FSWriteResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("FSWriteResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "result", js: "result", typ: u(undefined, r("FSWriteResultMessageResult")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "FSWriteResultMessageResult": o([
        { json: "conflict", js: "conflict", typ: true },
        { json: "current_hash", js: "current_hash", typ: u(undefined, "") },
        { json: "hash", js: "hash", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
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
    "GetPresentationRoundMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetPresentationRoundMessageCmd") },
        { json: "presentation_id", js: "presentation_id", typ: "" },
        { json: "seq", js: "seq", typ: u(undefined, 0) },
    ], "any"),
    "GetPresentationRoundResultMessage": o([
        { json: "comments", js: "comments", typ: u(undefined, a(r("CommentElement"))) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("GetPresentationRoundResultMessageEvent") },
        { json: "presentation", js: "presentation", typ: u(undefined, r("PresentationElement")) },
        { json: "round", js: "round", typ: u(undefined, r("Round")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "CommentElement": o([
        { json: "author", js: "author", typ: "" },
        { json: "content", js: "content", typ: "" },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "filepath", js: "filepath", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "line_end", js: "line_end", typ: 0 },
        { json: "line_start", js: "line_start", typ: 0 },
        { json: "round_id", js: "round_id", typ: "" },
        { json: "side", js: "side", typ: "" },
    ], "any"),
    "PresentationElement": o([
        { json: "created_at", js: "created_at", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "kind", js: "kind", typ: "" },
        { json: "latest_round_seq", js: "latest_round_seq", typ: 0 },
        { json: "latest_round_submitted", js: "latest_round_submitted", typ: true },
        { json: "repo_path", js: "repo_path", typ: "" },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "status", js: "status", typ: "" },
        { json: "ticket_id", js: "ticket_id", typ: u(undefined, "") },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "Round": o([
        { json: "base_sha", js: "base_sha", typ: "" },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "head_sha", js: "head_sha", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "manifest", js: "manifest", typ: r("Manifest") },
        { json: "presentation_id", js: "presentation_id", typ: "" },
        { json: "seq", js: "seq", typ: 0 },
        { json: "submitted_at", js: "submitted_at", typ: u(undefined, "") },
    ], "any"),
    "Manifest": o([
        { json: "files", js: "files", typ: a(r("FileObject")) },
        { json: "skip", js: "skip", typ: a("") },
        { json: "summary", js: "summary", typ: u(undefined, "") },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "FileObject": o([
        { json: "note", js: "note", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "GetPresentationsMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetPresentationsMessageCmd") },
    ], "any"),
    "GetPresentationsResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("GetPresentationsResultMessageEvent") },
        { json: "presentations", js: "presentations", typ: a(r("PresentationElement")) },
        { json: "success", js: "success", typ: true },
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
    "GetTicketMessage": o([
        { json: "cmd", js: "cmd", typ: r("GetTicketMessageCmd") },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
        { json: "ticket_id", js: "ticket_id", typ: "" },
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
        { json: "tickets", js: "tickets", typ: u(undefined, a(r("TicketElement"))) },
        { json: "warnings", js: "warnings", typ: u(undefined, a(r("WarningElement"))) },
        { json: "workspaces", js: "workspaces", typ: u(undefined, a(r("WorkspaceElement"))) },
    ], "any"),
    "RepoElement": o([
        { json: "collapsed", js: "collapsed", typ: true },
        { json: "muted", js: "muted", typ: true },
        { json: "repo", js: "repo", typ: "" },
    ], "any"),
    "TicketElement": o([
        { json: "activity", js: "activity", typ: a(r("ActivityElement")) },
        { json: "archived_at", js: "archived_at", typ: u(undefined, "") },
        { json: "assignee", js: "assignee", typ: "" },
        { json: "attachments", js: "attachments", typ: a(r("AttachmentElement")) },
        { json: "closed_at", js: "closed_at", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "cwd", js: "cwd", typ: "" },
        { json: "description", js: "description", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "last_agent_id", js: "last_agent_id", typ: "" },
        { json: "project_id", js: "project_id", typ: "" },
        { json: "reconciled_at", js: "reconciled_at", typ: u(undefined, "") },
        { json: "status", js: "status", typ: r("TicketStatus") },
        { json: "title", js: "title", typ: "" },
        { json: "updated_at", js: "updated_at", typ: "" },
    ], "any"),
    "ActivityElement": o([
        { json: "author", js: "author", typ: "" },
        { json: "comment", js: "comment", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "from_status", js: "from_status", typ: u(undefined, r("TicketStatus")) },
        { json: "id", js: "id", typ: 0 },
        { json: "kind", js: "kind", typ: r("TicketActivityKind") },
        { json: "to_status", js: "to_status", typ: u(undefined, r("TicketStatus")) },
    ], "any"),
    "AttachmentElement": o([
        { json: "created_at", js: "created_at", typ: "" },
        { json: "filename", js: "filename", typ: "" },
        { json: "id", js: "id", typ: 0 },
        { json: "note", js: "note", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
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
        { json: "pinned", js: "pinned", typ: true },
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
    "NotebookBacklinksMessage": o([
        { json: "cmd", js: "cmd", typ: r("NotebookBacklinksMessageCmd") },
        { json: "path", js: "path", typ: "" },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "NotebookBacklinksResultMessage": o([
        { json: "entries", js: "entries", typ: u(undefined, a(r("NotebookEntryElement"))) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("NotebookBacklinksResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "NotebookEntryElement": o([
        { json: "path", js: "path", typ: "" },
        { json: "size", js: "size", typ: 0 },
        { json: "summary", js: "summary", typ: u(undefined, "") },
        { json: "title", js: "title", typ: u(undefined, "") },
        { json: "type", js: "type", typ: u(undefined, "") },
        { json: "updated", js: "updated", typ: u(undefined, "") },
    ], "any"),
    "NotebookChangedMessage": o([
        { json: "event", js: "event", typ: r("NotebookChangedMessageEvent") },
        { json: "origin", js: "origin", typ: "" },
        { json: "paths", js: "paths", typ: a("") },
    ], "any"),
    "NotebookEntry": o([
        { json: "path", js: "path", typ: "" },
        { json: "size", js: "size", typ: 0 },
        { json: "summary", js: "summary", typ: u(undefined, "") },
        { json: "title", js: "title", typ: u(undefined, "") },
        { json: "type", js: "type", typ: u(undefined, "") },
        { json: "updated", js: "updated", typ: u(undefined, "") },
    ], "any"),
    "NotebookGuideMessage": o([
        { json: "cmd", js: "cmd", typ: r("NotebookGuideMessageCmd") },
        { json: "session_id", js: "session_id", typ: u(undefined, "") },
    ], "any"),
    "NotebookGuideResult": o([
        { json: "guidance", js: "guidance", typ: "" },
        { json: "root", js: "root", typ: "" },
        { json: "session_is_chief", js: "session_is_chief", typ: true },
    ], "any"),
    "NotebookListMessage": o([
        { json: "cmd", js: "cmd", typ: r("NotebookListMessageCmd") },
        { json: "prefix", js: "prefix", typ: u(undefined, "") },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "NotebookListResultMessage": o([
        { json: "entries", js: "entries", typ: u(undefined, a(r("NotebookEntryElement"))) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("NotebookListResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "NotebookReadMessage": o([
        { json: "cmd", js: "cmd", typ: r("NotebookReadMessageCmd") },
        { json: "path", js: "path", typ: "" },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "NotebookReadResult": o([
        { json: "content", js: "content", typ: "" },
        { json: "hash", js: "hash", typ: "" },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "NotebookReadResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("NotebookReadResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "result", js: "result", typ: u(undefined, r("NotebookReadObject")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "NotebookReadObject": o([
        { json: "content", js: "content", typ: "" },
        { json: "hash", js: "hash", typ: "" },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "NotebookSendToChiefMessage": o([
        { json: "cmd", js: "cmd", typ: r("NotebookSendToChiefMessageCmd") },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
        { json: "selection", js: "selection", typ: "" },
        { json: "source_path", js: "source_path", typ: u(undefined, "") },
    ], "any"),
    "NotebookSendToChiefResult": o([
        { json: "nudged", js: "nudged", typ: true },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "NotebookSendToChiefResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("NotebookSendToChiefResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "result", js: "result", typ: u(undefined, r("NotebookSendToChiefResultMessageResult")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "NotebookSendToChiefResultMessageResult": o([
        { json: "nudged", js: "nudged", typ: true },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "NotebookWriteMessage": o([
        { json: "base_hash", js: "base_hash", typ: u(undefined, "") },
        { json: "cmd", js: "cmd", typ: r("NotebookWriteMessageCmd") },
        { json: "content", js: "content", typ: "" },
        { json: "path", js: "path", typ: "" },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "NotebookWriteResult": o([
        { json: "conflict", js: "conflict", typ: true },
        { json: "current_hash", js: "current_hash", typ: u(undefined, "") },
        { json: "hash", js: "hash", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "NotebookWriteResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("NotebookWriteResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "result", js: "result", typ: u(undefined, r("NotebookWriteObject")) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "NotebookWriteObject": o([
        { json: "conflict", js: "conflict", typ: true },
        { json: "current_hash", js: "current_hash", typ: u(undefined, "") },
        { json: "hash", js: "hash", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "Notification": o([
        { json: "body", js: "body", typ: "" },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "detail", js: "detail", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "kind", js: "kind", typ: "" },
        { json: "read_at", js: "read_at", typ: "" },
        { json: "source_id", js: "source_id", typ: "" },
        { json: "source_kind", js: "source_kind", typ: "" },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "NotificationListMessage": o([
        { json: "cmd", js: "cmd", typ: r("NotificationListMessageCmd") },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "NotificationListResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("NotificationListResultMessageEvent") },
        { json: "notifications", js: "notifications", typ: u(undefined, a(r("NotificationElement"))) },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
        { json: "unread_count", js: "unread_count", typ: 0 },
    ], "any"),
    "NotificationElement": o([
        { json: "body", js: "body", typ: "" },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "detail", js: "detail", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "kind", js: "kind", typ: "" },
        { json: "read_at", js: "read_at", typ: "" },
        { json: "source_id", js: "source_id", typ: "" },
        { json: "source_kind", js: "source_kind", typ: "" },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "NotificationMarkReadMessage": o([
        { json: "cmd", js: "cmd", typ: r("NotificationMarkReadMessageCmd") },
        { json: "notification_id", js: "notification_id", typ: u(undefined, "") },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "NotificationMarkReadResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("NotificationMarkReadResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
        { json: "unread_count", js: "unread_count", typ: 0 },
    ], "any"),
    "NotificationsUpdatedMessage": o([
        { json: "event", js: "event", typ: r("NotificationsUpdatedMessageEvent") },
        { json: "unread_count", js: "unread_count", typ: 0 },
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
    "PinWorkspaceMessage": o([
        { json: "cmd", js: "cmd", typ: r("PinWorkspaceMessageCmd") },
        { json: "pinned", js: "pinned", typ: true },
        { json: "workspace_id", js: "workspace_id", typ: "" },
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
    "Presentation": o([
        { json: "created_at", js: "created_at", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "kind", js: "kind", typ: "" },
        { json: "latest_round_seq", js: "latest_round_seq", typ: 0 },
        { json: "latest_round_submitted", js: "latest_round_submitted", typ: true },
        { json: "repo_path", js: "repo_path", typ: "" },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "status", js: "status", typ: "" },
        { json: "ticket_id", js: "ticket_id", typ: u(undefined, "") },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "PresentationAddedMessage": o([
        { json: "event", js: "event", typ: r("PresentationAddedMessageEvent") },
        { json: "presentation", js: "presentation", typ: r("PresentationElement") },
    ], "any"),
    "PresentationComment": o([
        { json: "author", js: "author", typ: "" },
        { json: "content", js: "content", typ: "" },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "filepath", js: "filepath", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "line_end", js: "line_end", typ: 0 },
        { json: "line_start", js: "line_start", typ: 0 },
        { json: "round_id", js: "round_id", typ: "" },
        { json: "side", js: "side", typ: "" },
    ], "any"),
    "PresentationRound": o([
        { json: "base_sha", js: "base_sha", typ: "" },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "head_sha", js: "head_sha", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "manifest", js: "manifest", typ: r("Manifest") },
        { json: "presentation_id", js: "presentation_id", typ: "" },
        { json: "seq", js: "seq", typ: 0 },
        { json: "submitted_at", js: "submitted_at", typ: u(undefined, "") },
    ], "any"),
    "PresentationUpdatedMessage": o([
        { json: "event", js: "event", typ: r("PresentationUpdatedMessageEvent") },
        { json: "presentation", js: "presentation", typ: r("PresentationElement") },
    ], "any"),
    "PresentCommentInput": o([
        { json: "content", js: "content", typ: "" },
        { json: "filepath", js: "filepath", typ: "" },
        { json: "line_end", js: "line_end", typ: 0 },
        { json: "line_start", js: "line_start", typ: 0 },
        { json: "side", js: "side", typ: "" },
    ], "any"),
    "PresentFeedbackMessage": o([
        { json: "cmd", js: "cmd", typ: r("PresentFeedbackMessageCmd") },
        { json: "presentation_id", js: "presentation_id", typ: "" },
        { json: "seq", js: "seq", typ: u(undefined, 0) },
    ], "any"),
    "PresentFeedbackResult": o([
        { json: "markdown", js: "markdown", typ: "" },
        { json: "seq", js: "seq", typ: 0 },
        { json: "submitted", js: "submitted", typ: true },
    ], "any"),
    "PresentFile": o([
        { json: "note", js: "note", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "PresentManifestView": o([
        { json: "files", js: "files", typ: a(r("FileObject")) },
        { json: "skip", js: "skip", typ: a("") },
        { json: "summary", js: "summary", typ: u(undefined, "") },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "PresentOpenMessage": o([
        { json: "cmd", js: "cmd", typ: r("PresentOpenMessageCmd") },
        { json: "manifest_yaml", js: "manifest_yaml", typ: "" },
        { json: "presentation_id", js: "presentation_id", typ: u(undefined, "") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
        { json: "ticket_id", js: "ticket_id", typ: u(undefined, "") },
    ], "any"),
    "PresentOpenResult": o([
        { json: "base_sha", js: "base_sha", typ: "" },
        { json: "head_sha", js: "head_sha", typ: "" },
        { json: "presentation_id", js: "presentation_id", typ: "" },
        { json: "round_id", js: "round_id", typ: "" },
        { json: "seq", js: "seq", typ: 0 },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "PresentSubmitRoundMessage": o([
        { json: "cmd", js: "cmd", typ: r("PresentSubmitRoundMessageCmd") },
        { json: "comments", js: "comments", typ: a(r("CommentObject")) },
        { json: "handback", js: "handback", typ: true },
        { json: "round_id", js: "round_id", typ: "" },
    ], "any"),
    "CommentObject": o([
        { json: "content", js: "content", typ: "" },
        { json: "filepath", js: "filepath", typ: "" },
        { json: "line_end", js: "line_end", typ: 0 },
        { json: "line_start", js: "line_start", typ: 0 },
        { json: "side", js: "side", typ: "" },
    ], "any"),
    "PresentSubmitRoundResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("PresentSubmitRoundResultMessageEvent") },
        { json: "round_id", js: "round_id", typ: "" },
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
        { json: "data", js: "data", typ: u(undefined, "") },
        { json: "delegate_result", js: "delegate_result", typ: u(undefined, r("DelegateResultObject")) },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "notebook_entries", js: "notebook_entries", typ: u(undefined, a(r("NotebookEntryElement"))) },
        { json: "notebook_guide", js: "notebook_guide", typ: u(undefined, r("NotebookGuide")) },
        { json: "notebook_read", js: "notebook_read", typ: u(undefined, r("NotebookReadObject")) },
        { json: "notebook_write", js: "notebook_write", typ: u(undefined, r("NotebookWriteObject")) },
        { json: "ok", js: "ok", typ: true },
        { json: "present_feedback_result", js: "present_feedback_result", typ: u(undefined, r("PresentFeedbackResultObject")) },
        { json: "present_open_result", js: "present_open_result", typ: u(undefined, r("PresentOpenResultObject")) },
        { json: "prs", js: "prs", typ: u(undefined, a(r("PRElement"))) },
        { json: "repos", js: "repos", typ: u(undefined, a(r("RepoElement"))) },
        { json: "sessions", js: "sessions", typ: u(undefined, a(r("SessionElement"))) },
        { json: "ticket_attach_result", js: "ticket_attach_result", typ: u(undefined, r("TicketAttachResultObject")) },
        { json: "ticket_comment_result", js: "ticket_comment_result", typ: u(undefined, r("TicketCommentResultObject")) },
        { json: "ticket_create_result", js: "ticket_create_result", typ: u(undefined, r("TicketCreateResultObject")) },
        { json: "ticket_inbox_result", js: "ticket_inbox_result", typ: u(undefined, r("TicketInboxResultObject")) },
        { json: "ticket_list_result", js: "ticket_list_result", typ: u(undefined, r("TicketListResultObject")) },
        { json: "ticket_status_result", js: "ticket_status_result", typ: u(undefined, r("TicketStatusResultObject")) },
        { json: "ticket_subscribe_result", js: "ticket_subscribe_result", typ: u(undefined, r("TicketSubscribeResultObject")) },
        { json: "ticket_take_result", js: "ticket_take_result", typ: u(undefined, r("TicketTakeResultObject")) },
        { json: "ticket_unsubscribe_result", js: "ticket_unsubscribe_result", typ: u(undefined, r("TicketUnsubscribeResultObject")) },
        { json: "workspace_context_maintenance_result", js: "workspace_context_maintenance_result", typ: u(undefined, r("WorkspaceContextMaintenanceResultObject")) },
        { json: "workspace_context_result", js: "workspace_context_result", typ: u(undefined, r("WorkspaceContextResultObject")) },
        { json: "workspace_contexts", js: "workspace_contexts", typ: u(undefined, a(r("WorkspaceContextElement"))) },
        { json: "workspaces", js: "workspaces", typ: u(undefined, a(r("WorkspaceElement"))) },
    ], "any"),
    "NotebookGuide": o([
        { json: "guidance", js: "guidance", typ: "" },
        { json: "root", js: "root", typ: "" },
        { json: "session_is_chief", js: "session_is_chief", typ: true },
    ], "any"),
    "PresentFeedbackResultObject": o([
        { json: "markdown", js: "markdown", typ: "" },
        { json: "seq", js: "seq", typ: 0 },
        { json: "submitted", js: "submitted", typ: true },
    ], "any"),
    "PresentOpenResultObject": o([
        { json: "base_sha", js: "base_sha", typ: "" },
        { json: "head_sha", js: "head_sha", typ: "" },
        { json: "presentation_id", js: "presentation_id", typ: "" },
        { json: "round_id", js: "round_id", typ: "" },
        { json: "seq", js: "seq", typ: 0 },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "TicketAttachResultObject": o([
        { json: "filename", js: "filename", typ: "" },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketCommentResultObject": o([
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketCreateResultObject": o([
        { json: "status", js: "status", typ: r("TicketStatus") },
        { json: "ticket_id", js: "ticket_id", typ: "" },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "TicketInboxResultObject": o([
        { json: "bundles", js: "bundles", typ: a(r("BundleElement")) },
    ], "any"),
    "BundleElement": o([
        { json: "events", js: "events", typ: a(r("EventElement")) },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "EventElement": o([
        { json: "author", js: "author", typ: "" },
        { json: "comment", js: "comment", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "detail", js: "detail", typ: u(undefined, "") },
        { json: "from_status", js: "from_status", typ: u(undefined, r("TicketStatus")) },
        { json: "kind", js: "kind", typ: r("TicketEventKind") },
        { json: "ticket_id", js: "ticket_id", typ: "" },
        { json: "to_status", js: "to_status", typ: u(undefined, r("TicketStatus")) },
    ], "any"),
    "TicketListResultObject": o([
        { json: "tickets", js: "tickets", typ: a(r("TicketElement")) },
    ], "any"),
    "TicketStatusResultObject": o([
        { json: "status", js: "status", typ: r("TicketStatus") },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketSubscribeResultObject": o([
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketTakeResultObject": o([
        { json: "previous_assignee", js: "previous_assignee", typ: "" },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketUnsubscribeResultObject": o([
        { json: "ticket_id", js: "ticket_id", typ: "" },
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
    "ReviewState": o([
        { json: "branch", js: "branch", typ: "" },
        { json: "repo_path", js: "repo_path", typ: "" },
        { json: "review_id", js: "review_id", typ: "" },
        { json: "viewed_files", js: "viewed_files", typ: a("") },
    ], "any"),
    "RuntimeRespawnedMessage": o([
        { json: "event", js: "event", typ: r("RuntimeRespawnedMessageEvent") },
        { json: "id", js: "id", typ: "" },
    ], "any"),
    "Session": o([
        { json: "agent", js: "agent", typ: "" },
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "chief_of_staff", js: "chief_of_staff", typ: u(undefined, true) },
        { json: "delegated_from_chief", js: "delegated_from_chief", typ: u(undefined, true) },
        { json: "directory", js: "directory", typ: "" },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "is_worktree", js: "is_worktree", typ: u(undefined, true) },
        { json: "label", js: "label", typ: "" },
        { json: "last_seen", js: "last_seen", typ: "" },
        { json: "main_repo", js: "main_repo", typ: u(undefined, "") },
        { json: "needs_review_after_long_run", js: "needs_review_after_long_run", typ: u(undefined, true) },
        { json: "nudge_fires_at", js: "nudge_fires_at", typ: u(undefined, "") },
        { json: "recoverable", js: "recoverable", typ: u(undefined, true) },
        { json: "state", js: "state", typ: r("WorkspaceStatus") },
        { json: "state_since", js: "state_since", typ: "" },
        { json: "state_updated_at", js: "state_updated_at", typ: "" },
        { json: "ticket_unread", js: "ticket_unread", typ: u(undefined, true) },
        { json: "todos", js: "todos", typ: u(undefined, a("")) },
        { json: "workspace_id", js: "workspace_id", typ: "" },
        { json: "workspace_muted", js: "workspace_muted", typ: u(undefined, true) },
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
    "SetTicketStatusMessage": o([
        { json: "cmd", js: "cmd", typ: r("SetTicketStatusMessageCmd") },
        { json: "comment", js: "comment", typ: u(undefined, "") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
        { json: "work_state", js: "work_state", typ: r("DispatchWorkState") },
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
        { json: "chief_of_staff", js: "chief_of_staff", typ: u(undefined, true) },
        { json: "claude_executable", js: "claude_executable", typ: u(undefined, "") },
        { json: "cmd", js: "cmd", typ: r("SpawnSessionMessageCmd") },
        { json: "codex_executable", js: "codex_executable", typ: u(undefined, "") },
        { json: "cols", js: "cols", typ: 0 },
        { json: "copilot_executable", js: "copilot_executable", typ: u(undefined, "") },
        { json: "cwd", js: "cwd", typ: "" },
        { json: "effort", js: "effort", typ: u(undefined, "") },
        { json: "endpoint_id", js: "endpoint_id", typ: u(undefined, "") },
        { json: "executable", js: "executable", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "initial_prompt", js: "initial_prompt", typ: u(undefined, "") },
        { json: "label", js: "label", typ: u(undefined, "") },
        { json: "model", js: "model", typ: u(undefined, "") },
        { json: "resume_picker", js: "resume_picker", typ: u(undefined, true) },
        { json: "resume_session_id", js: "resume_session_id", typ: u(undefined, "") },
        { json: "rows", js: "rows", typ: 0 },
        { json: "workspace_id", js: "workspace_id", typ: "" },
        { json: "yolo_mode", js: "yolo_mode", typ: u(undefined, true) },
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
    "Task": o([
        { json: "attempts", js: "attempts", typ: 0 },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "kind", js: "kind", typ: "" },
        { json: "last_error", js: "last_error", typ: u(undefined, "") },
        { json: "next_attempt_at", js: "next_attempt_at", typ: "" },
        { json: "state", js: "state", typ: "" },
        { json: "subject", js: "subject", typ: "" },
        { json: "updated_at", js: "updated_at", typ: "" },
    ], "any"),
    "TaskListMessage": o([
        { json: "cmd", js: "cmd", typ: r("TaskListMessageCmd") },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
    ], "any"),
    "TaskListResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("TaskListResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
        { json: "tasks", js: "tasks", typ: u(undefined, a(r("TaskElement"))) },
    ], "any"),
    "TaskElement": o([
        { json: "attempts", js: "attempts", typ: 0 },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "kind", js: "kind", typ: "" },
        { json: "last_error", js: "last_error", typ: u(undefined, "") },
        { json: "next_attempt_at", js: "next_attempt_at", typ: "" },
        { json: "state", js: "state", typ: "" },
        { json: "subject", js: "subject", typ: "" },
        { json: "updated_at", js: "updated_at", typ: "" },
    ], "any"),
    "TaskRetryMessage": o([
        { json: "cmd", js: "cmd", typ: r("TaskRetryMessageCmd") },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
        { json: "task_id", js: "task_id", typ: "" },
    ], "any"),
    "TaskRetryResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("TaskRetryResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
        { json: "task", js: "task", typ: u(undefined, r("TaskElement")) },
    ], "any"),
    "TasksChangedMessage": o([
        { json: "event", js: "event", typ: r("TasksChangedMessageEvent") },
    ], "any"),
    "Ticket": o([
        { json: "activity", js: "activity", typ: a(r("ActivityElement")) },
        { json: "archived_at", js: "archived_at", typ: u(undefined, "") },
        { json: "assignee", js: "assignee", typ: "" },
        { json: "attachments", js: "attachments", typ: a(r("AttachmentElement")) },
        { json: "closed_at", js: "closed_at", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "cwd", js: "cwd", typ: "" },
        { json: "description", js: "description", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "last_agent_id", js: "last_agent_id", typ: "" },
        { json: "project_id", js: "project_id", typ: "" },
        { json: "reconciled_at", js: "reconciled_at", typ: u(undefined, "") },
        { json: "status", js: "status", typ: r("TicketStatus") },
        { json: "title", js: "title", typ: "" },
        { json: "updated_at", js: "updated_at", typ: "" },
    ], "any"),
    "TicketActionResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("TicketActionResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "TicketActivity": o([
        { json: "author", js: "author", typ: "" },
        { json: "comment", js: "comment", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "from_status", js: "from_status", typ: u(undefined, r("TicketStatus")) },
        { json: "id", js: "id", typ: 0 },
        { json: "kind", js: "kind", typ: r("TicketActivityKind") },
        { json: "to_status", js: "to_status", typ: u(undefined, r("TicketStatus")) },
    ], "any"),
    "TicketAddCommentMessage": o([
        { json: "cmd", js: "cmd", typ: r("TicketAddCommentMessageCmd") },
        { json: "comment", js: "comment", typ: "" },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketAttachment": o([
        { json: "created_at", js: "created_at", typ: "" },
        { json: "filename", js: "filename", typ: "" },
        { json: "id", js: "id", typ: 0 },
        { json: "note", js: "note", typ: u(undefined, "") },
        { json: "path", js: "path", typ: "" },
    ], "any"),
    "TicketAttachMessage": o([
        { json: "cmd", js: "cmd", typ: r("TicketAttachMessageCmd") },
        { json: "filename", js: "filename", typ: "" },
        { json: "note", js: "note", typ: u(undefined, "") },
        { json: "source_path", js: "source_path", typ: "" },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
    "TicketAttachResult": o([
        { json: "filename", js: "filename", typ: "" },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketChangeStatusMessage": o([
        { json: "cmd", js: "cmd", typ: r("TicketChangeStatusMessageCmd") },
        { json: "comment", js: "comment", typ: u(undefined, "") },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
        { json: "status", js: "status", typ: r("TicketStatus") },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketCommentMessage": o([
        { json: "cmd", js: "cmd", typ: r("TicketCommentMessageCmd") },
        { json: "comment", js: "comment", typ: "" },
        { json: "source_session_id", js: "source_session_id", typ: "" },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketCommentResult": o([
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketCreateMessage": o([
        { json: "cmd", js: "cmd", typ: r("TicketCreateMessageCmd") },
        { json: "description", js: "description", typ: u(undefined, "") },
        { json: "id", js: "id", typ: u(undefined, "") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "TicketCreateResult": o([
        { json: "status", js: "status", typ: r("TicketStatus") },
        { json: "ticket_id", js: "ticket_id", typ: "" },
        { json: "title", js: "title", typ: "" },
    ], "any"),
    "TicketEditDescriptionMessage": o([
        { json: "cmd", js: "cmd", typ: r("TicketEditDescriptionMessageCmd") },
        { json: "description", js: "description", typ: "" },
        { json: "request_id", js: "request_id", typ: u(undefined, "") },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketEvent": o([
        { json: "author", js: "author", typ: "" },
        { json: "comment", js: "comment", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "detail", js: "detail", typ: u(undefined, "") },
        { json: "from_status", js: "from_status", typ: u(undefined, r("TicketStatus")) },
        { json: "kind", js: "kind", typ: r("TicketEventKind") },
        { json: "ticket_id", js: "ticket_id", typ: "" },
        { json: "to_status", js: "to_status", typ: u(undefined, r("TicketStatus")) },
    ], "any"),
    "TicketEventBundle": o([
        { json: "events", js: "events", typ: a(r("EventElement")) },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketInboxMessage": o([
        { json: "cmd", js: "cmd", typ: r("TicketInboxMessageCmd") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
    ], "any"),
    "TicketInboxResult": o([
        { json: "bundles", js: "bundles", typ: a(r("BundleElement")) },
    ], "any"),
    "TicketListMessage": o([
        { json: "cmd", js: "cmd", typ: r("TicketListMessageCmd") },
        { json: "include_archived", js: "include_archived", typ: u(undefined, true) },
        { json: "source_session_id", js: "source_session_id", typ: u(undefined, "") },
        { json: "status", js: "status", typ: u(undefined, "") },
    ], "any"),
    "TicketListResult": o([
        { json: "tickets", js: "tickets", typ: a(r("TicketElement")) },
    ], "any"),
    "TicketResultMessage": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("TicketResultMessageEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "success", js: "success", typ: true },
        { json: "ticket", js: "ticket", typ: u(undefined, r("TicketElement")) },
    ], "any"),
    "TicketStatusResult": o([
        { json: "status", js: "status", typ: r("TicketStatus") },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketSubscribeMessage": o([
        { json: "cmd", js: "cmd", typ: r("TicketSubscribeMessageCmd") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketSubscribeResult": o([
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketsUpdatedMessage": o([
        { json: "event", js: "event", typ: r("TicketsUpdatedMessageEvent") },
        { json: "tickets", js: "tickets", typ: a(r("TicketElement")) },
    ], "any"),
    "TicketTakeMessage": o([
        { json: "cmd", js: "cmd", typ: r("TicketTakeMessageCmd") },
        { json: "confirm", js: "confirm", typ: u(undefined, true) },
        { json: "source_session_id", js: "source_session_id", typ: "" },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketTakeResult": o([
        { json: "previous_assignee", js: "previous_assignee", typ: "" },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketUnsubscribeMessage": o([
        { json: "cmd", js: "cmd", typ: r("TicketUnsubscribeMessageCmd") },
        { json: "source_session_id", js: "source_session_id", typ: "" },
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TicketUnsubscribeResult": o([
        { json: "ticket_id", js: "ticket_id", typ: "" },
    ], "any"),
    "TodosMessage": o([
        { json: "cmd", js: "cmd", typ: r("TodosMessageCmd") },
        { json: "id", js: "id", typ: "" },
        { json: "todos", js: "todos", typ: a("") },
    ], "any"),
    "TriggerNudgeMessage": o([
        { json: "cmd", js: "cmd", typ: r("TriggerNudgeMessageCmd") },
        { json: "session_id", js: "session_id", typ: "" },
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
    "WebSocketEvent": o([
        { json: "action", js: "action", typ: u(undefined, "") },
        { json: "authors", js: "authors", typ: u(undefined, a(r("AuthorElement"))) },
        { json: "base_ref", js: "base_ref", typ: u(undefined, "") },
        { json: "branch", js: "branch", typ: u(undefined, "") },
        { json: "branches", js: "branches", typ: u(undefined, a(r("BranchElement"))) },
        { json: "chief_of_staff", js: "chief_of_staff", typ: u(undefined, true) },
        { json: "cloned", js: "cloned", typ: u(undefined, true) },
        { json: "cmd", js: "cmd", typ: u(undefined, "") },
        { json: "cols", js: "cols", typ: u(undefined, 0) },
        { json: "conflict", js: "conflict", typ: u(undefined, true) },
        { json: "content", js: "content", typ: u(undefined, "") },
        { json: "data", js: "data", typ: u(undefined, "") },
        { json: "directory", js: "directory", typ: u(undefined, "") },
        { json: "dirty", js: "dirty", typ: u(undefined, true) },
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
        { json: "ticket", js: "ticket", typ: u(undefined, r("TicketElement")) },
        { json: "tickets", js: "tickets", typ: u(undefined, a(r("TicketElement"))) },
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
    "WorkflowActionResultMessage": o([
        { json: "action", js: "action", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("WorkflowActionResultMessageEvent") },
        { json: "run", js: "run", typ: u(undefined, r("Run")) },
        { json: "run_id", js: "run_id", typ: u(undefined, "") },
        { json: "runs", js: "runs", typ: u(undefined, a(r("Run"))) },
        { json: "success", js: "success", typ: true },
    ], "any"),
    "Run": o([
        { json: "agent_calls", js: "agent_calls", typ: u(undefined, a(r("Call"))) },
        { json: "args_json", js: "args_json", typ: u(undefined, "") },
        { json: "completed_at", js: "completed_at", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "harness", js: "harness", typ: u(undefined, "") },
        { json: "last_error", js: "last_error", typ: u(undefined, "") },
        { json: "phase", js: "phase", typ: u(undefined, "") },
        { json: "result_json", js: "result_json", typ: u(undefined, "") },
        { json: "resumable", js: "resumable", typ: true },
        { json: "run_id", js: "run_id", typ: "" },
        { json: "script_hash", js: "script_hash", typ: "" },
        { json: "script_path", js: "script_path", typ: "" },
        { json: "session_id", js: "session_id", typ: u(undefined, "") },
        { json: "status", js: "status", typ: r("WorkflowRunStatus") },
        { json: "updated_at", js: "updated_at", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: u(undefined, "") },
    ], "any"),
    "Call": o([
        { json: "agent_type", js: "agent_type", typ: u(undefined, "") },
        { json: "completed_at", js: "completed_at", typ: u(undefined, "") },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "label", js: "label", typ: u(undefined, "") },
        { json: "ordinal", js: "ordinal", typ: "" },
        { json: "phase", js: "phase", typ: u(undefined, "") },
        { json: "prompt_hash", js: "prompt_hash", typ: u(undefined, "") },
        { json: "resolved_harness", js: "resolved_harness", typ: u(undefined, "") },
        { json: "resolved_model", js: "resolved_model", typ: u(undefined, "") },
        { json: "result_json", js: "result_json", typ: u(undefined, "") },
        { json: "result_path", js: "result_path", typ: u(undefined, "") },
        { json: "run_id", js: "run_id", typ: "" },
        { json: "schema_hash", js: "schema_hash", typ: u(undefined, "") },
        { json: "started_at", js: "started_at", typ: u(undefined, "") },
        { json: "status", js: "status", typ: r("WorkflowAgentCallStatus") },
    ], "any"),
    "WorkflowAgentCall": o([
        { json: "agent_type", js: "agent_type", typ: u(undefined, "") },
        { json: "completed_at", js: "completed_at", typ: u(undefined, "") },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "label", js: "label", typ: u(undefined, "") },
        { json: "ordinal", js: "ordinal", typ: "" },
        { json: "phase", js: "phase", typ: u(undefined, "") },
        { json: "prompt_hash", js: "prompt_hash", typ: u(undefined, "") },
        { json: "resolved_harness", js: "resolved_harness", typ: u(undefined, "") },
        { json: "resolved_model", js: "resolved_model", typ: u(undefined, "") },
        { json: "result_json", js: "result_json", typ: u(undefined, "") },
        { json: "result_path", js: "result_path", typ: u(undefined, "") },
        { json: "run_id", js: "run_id", typ: "" },
        { json: "schema_hash", js: "schema_hash", typ: u(undefined, "") },
        { json: "started_at", js: "started_at", typ: u(undefined, "") },
        { json: "status", js: "status", typ: r("WorkflowAgentCallStatus") },
    ], "any"),
    "WorkflowCallUpsertMessage": o([
        { json: "call", js: "call", typ: r("Call") },
        { json: "cmd", js: "cmd", typ: r("WorkflowCallUpsertMessageCmd") },
        { json: "run_id", js: "run_id", typ: "" },
    ], "any"),
    "WorkflowRun": o([
        { json: "agent_calls", js: "agent_calls", typ: u(undefined, a(r("Call"))) },
        { json: "args_json", js: "args_json", typ: u(undefined, "") },
        { json: "completed_at", js: "completed_at", typ: u(undefined, "") },
        { json: "created_at", js: "created_at", typ: "" },
        { json: "harness", js: "harness", typ: u(undefined, "") },
        { json: "last_error", js: "last_error", typ: u(undefined, "") },
        { json: "phase", js: "phase", typ: u(undefined, "") },
        { json: "result_json", js: "result_json", typ: u(undefined, "") },
        { json: "resumable", js: "resumable", typ: true },
        { json: "run_id", js: "run_id", typ: "" },
        { json: "script_hash", js: "script_hash", typ: "" },
        { json: "script_path", js: "script_path", typ: "" },
        { json: "session_id", js: "session_id", typ: u(undefined, "") },
        { json: "status", js: "status", typ: r("WorkflowRunStatus") },
        { json: "updated_at", js: "updated_at", typ: "" },
        { json: "workspace_id", js: "workspace_id", typ: u(undefined, "") },
    ], "any"),
    "WorkflowRunCancelMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkflowRunCancelMessageCmd") },
        { json: "run_id", js: "run_id", typ: "" },
    ], "any"),
    "WorkflowRunGetMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkflowRunGetMessageCmd") },
        { json: "run_id", js: "run_id", typ: "" },
    ], "any"),
    "WorkflowRunListMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkflowRunListMessageCmd") },
        { json: "session_id", js: "session_id", typ: u(undefined, "") },
        { json: "workspace_id", js: "workspace_id", typ: u(undefined, "") },
    ], "any"),
    "WorkflowRunUpdatedMessage": o([
        { json: "event", js: "event", typ: r("WorkflowRunUpdatedMessageEvent") },
        { json: "run", js: "run", typ: r("Run") },
    ], "any"),
    "WorkflowRunUpsertMessage": o([
        { json: "cmd", js: "cmd", typ: r("WorkflowRunUpsertMessageCmd") },
        { json: "run", js: "run", typ: r("Run") },
    ], "any"),
    "Workspace": o([
        { json: "directory", js: "directory", typ: "" },
        { json: "id", js: "id", typ: "" },
        { json: "layout", js: "layout", typ: u(undefined, r("Layout")) },
        { json: "muted", js: "muted", typ: true },
        { json: "pinned", js: "pinned", typ: true },
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
    "AddCommentMessageCmd": [
        "add_comment",
    ],
    "AddCommentResultMessageEvent": [
        "add_comment_result",
    ],
    "AddEndpointMessageCmd": [
        "add_endpoint",
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
    "FSChangedMessageEvent": [
        "fs_changed",
    ],
    "FSExistsMessageCmd": [
        "fs_exists",
    ],
    "FSExistsResultMessageEvent": [
        "fs_exists_result",
    ],
    "FSListMessageCmd": [
        "fs_list",
    ],
    "FSListResultMessageEvent": [
        "fs_list_result",
    ],
    "FSReadMessageCmd": [
        "fs_read",
    ],
    "FSReadResultMessageEvent": [
        "fs_read_result",
    ],
    "FSWriteMessageCmd": [
        "fs_write",
    ],
    "FSWriteResultMessageEvent": [
        "fs_write_result",
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
    "GetPresentationRoundMessageCmd": [
        "get_presentation_round",
    ],
    "GetPresentationRoundResultMessageEvent": [
        "get_presentation_round_result",
    ],
    "GetPresentationsMessageCmd": [
        "get_presentations",
    ],
    "GetPresentationsResultMessageEvent": [
        "get_presentations_result",
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
    "GetScreenSnapshotMessageCmd": [
        "get_screen_snapshot",
    ],
    "GetScreenSnapshotResultMessageEvent": [
        "get_screen_snapshot_result",
    ],
    "GetSettingsMessageCmd": [
        "get_settings",
    ],
    "GetTicketMessageCmd": [
        "get_ticket",
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
    "TicketStatus": [
        "blocked",
        "crashed",
        "done",
        "failed",
        "in_review",
        "todo",
        "working",
    ],
    "TicketActivityKind": [
        "comment",
        "status_change",
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
    "NotebookBacklinksMessageCmd": [
        "notebook_backlinks",
    ],
    "NotebookBacklinksResultMessageEvent": [
        "notebook_backlinks_result",
    ],
    "NotebookChangedMessageEvent": [
        "notebook_changed",
    ],
    "NotebookGuideMessageCmd": [
        "notebook_guide",
    ],
    "NotebookListMessageCmd": [
        "notebook_list",
    ],
    "NotebookListResultMessageEvent": [
        "notebook_list_result",
    ],
    "NotebookReadMessageCmd": [
        "notebook_read",
    ],
    "NotebookReadResultMessageEvent": [
        "notebook_read_result",
    ],
    "NotebookSendToChiefMessageCmd": [
        "notebook_send_to_chief",
    ],
    "NotebookSendToChiefResultMessageEvent": [
        "notebook_send_to_chief_result",
    ],
    "NotebookWriteMessageCmd": [
        "notebook_write",
    ],
    "NotebookWriteResultMessageEvent": [
        "notebook_write_result",
    ],
    "NotificationListMessageCmd": [
        "notification_list",
    ],
    "NotificationListResultMessageEvent": [
        "notification_list_result",
    ],
    "NotificationMarkReadMessageCmd": [
        "notification_mark_read",
    ],
    "NotificationMarkReadResultMessageEvent": [
        "notification_mark_read_result",
    ],
    "NotificationsUpdatedMessageEvent": [
        "notifications_updated",
    ],
    "OpenBrowserMessageCmd": [
        "open_browser",
    ],
    "OpenMarkdownMessageCmd": [
        "open_markdown",
    ],
    "PinWorkspaceMessageCmd": [
        "pin_workspace",
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
    "PresentationAddedMessageEvent": [
        "presentation_added",
    ],
    "PresentationUpdatedMessageEvent": [
        "presentation_updated",
    ],
    "PresentFeedbackMessageCmd": [
        "present_feedback",
    ],
    "PresentOpenMessageCmd": [
        "present_open",
    ],
    "PresentSubmitRoundMessageCmd": [
        "present_submit_round",
    ],
    "PresentSubmitRoundResultMessageEvent": [
        "present_submit_round_result",
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
    "ReposUpdatedMessageEvent": [
        "repos_updated",
    ],
    "ResolveCommentMessageCmd": [
        "resolve_comment",
    ],
    "ResolveCommentResultMessageEvent": [
        "resolve_comment_result",
    ],
    "TicketEventKind": [
        "assigned",
        "attachment_added",
        "commented",
        "created",
        "description_edited",
        "status_changed",
    ],
    "WorkspaceContextMaintenanceAction": [
        "compact",
        "rollback",
    ],
    "RuntimeRespawnedMessageEvent": [
        "runtime_respawned",
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
    "SetSessionResumeIDMessageCmd": [
        "set_session_resume_id",
    ],
    "SetSettingMessageCmd": [
        "set_setting",
    ],
    "SetTicketStatusMessageCmd": [
        "set_ticket_status",
    ],
    "DispatchWorkState": [
        "completed",
        "failed",
        "in_progress",
        "needs_input",
        "ready_for_review",
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
    "StateMessageCmd": [
        "state",
    ],
    "StopMessageCmd": [
        "stop",
    ],
    "SubscribeGitStatusMessageCmd": [
        "subscribe_git_status",
    ],
    "TaskListMessageCmd": [
        "task_list",
    ],
    "TaskListResultMessageEvent": [
        "task_list_result",
    ],
    "TaskRetryMessageCmd": [
        "task_retry",
    ],
    "TaskRetryResultMessageEvent": [
        "task_retry_result",
    ],
    "TasksChangedMessageEvent": [
        "tasks_changed",
    ],
    "TicketActionResultMessageEvent": [
        "ticket_action_result",
    ],
    "TicketAddCommentMessageCmd": [
        "ticket_add_comment",
    ],
    "TicketAttachMessageCmd": [
        "ticket_attach",
    ],
    "TicketChangeStatusMessageCmd": [
        "ticket_change_status",
    ],
    "TicketCommentMessageCmd": [
        "ticket_comment",
    ],
    "TicketCreateMessageCmd": [
        "ticket_create",
    ],
    "TicketEditDescriptionMessageCmd": [
        "ticket_edit_description",
    ],
    "TicketInboxMessageCmd": [
        "ticket_inbox",
    ],
    "TicketListMessageCmd": [
        "ticket_list",
    ],
    "TicketResultMessageEvent": [
        "ticket_result",
    ],
    "TicketSubscribeMessageCmd": [
        "ticket_subscribe",
    ],
    "TicketsUpdatedMessageEvent": [
        "tickets_updated",
    ],
    "TicketTakeMessageCmd": [
        "ticket_take",
    ],
    "TicketUnsubscribeMessageCmd": [
        "ticket_unsubscribe",
    ],
    "TodosMessageCmd": [
        "todos",
    ],
    "TriggerNudgeMessageCmd": [
        "trigger_nudge",
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
    "WorkflowActionResultMessageEvent": [
        "workflow_action_result",
    ],
    "WorkflowAgentCallStatus": [
        "errored",
        "ok",
        "running",
        "skipped",
    ],
    "WorkflowRunStatus": [
        "canceled",
        "completed",
        "failed",
        "running",
    ],
    "WorkflowCallUpsertMessageCmd": [
        "workflow_call_upsert",
    ],
    "WorkflowRunCancelMessageCmd": [
        "workflow_run_cancel",
    ],
    "WorkflowRunGetMessageCmd": [
        "workflow_run_get",
    ],
    "WorkflowRunListMessageCmd": [
        "workflow_run_list",
    ],
    "WorkflowRunUpdatedMessageEvent": [
        "workflow_run_updated",
    ],
    "WorkflowRunUpsertMessageCmd": [
        "workflow_run_upsert",
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
