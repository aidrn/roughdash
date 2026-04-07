export type User = {
  id: string
  username: string
  role: string
}

export type StatusResponse = {
  setupRequired: boolean
  user?: User
}

export type Helper = {
  id: string
  machineId: string
  name: string
  platform: string
  online: boolean
  pairedAt: string
  lastSeenAt?: string
}

export type FileEntry = {
  name: string
  path: string
  isDir: boolean
  size: number
  modTime: string
}

export type DashboardResponse = {
  activeJobs: number
  totalJobs: number
  helpers: Helper[]
  nasRoot: string
}

export type Job = {
  id: string
  type: string
  status: string
  summary: string
  error?: string
  progress: number
  createdAt: string
  updatedAt: string
  startedAt?: string
  finishedAt?: string
}

export type JobEvent = {
  id: number
  jobId: string
  level: string
  message: string
  createdAt: string
}

export type NotificationSettings = {
  browserEnabled: boolean
  telegramEnabled: boolean
  telegramBotToken: string
  telegramChatId: string
}

export type AuditEntry = {
  id: number
  action: string
  actor: string
  target: string
  details: string
  createdAt: string
}

export type IngestPreview = {
  targetPath: string
  estimatedSize: number
  files: Array<{
    sourcePath: string
    destinationPath: string
    kind: string
    size: number
  }>
  skipped: Array<{
    path: string
    reason: string
  }>
}

export type DownloadPreview = {
  groups: Array<{
    name: string
    targetPath: string
    videos: Array<{
      link: string
      videoId: string
      title: string
      uploader: string
      playlistTitle?: string
      qualityLabel: string
      finalPath: string
    }>
  }>
  duplicates?: Array<{
    videoId: string
    title: string
    uploader: string
    matchingJobId: string
    matchingStatus: string
    targetPath: string
    active: boolean
  }>
}
