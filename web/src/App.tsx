import { useEffect, useMemo, useState } from 'react'
import type { FormEvent, ReactNode } from 'react'
import { api } from './api'
import type {
  AuditEntry,
  DashboardResponse,
  DownloadPreview,
  FileEntry,
  Helper,
  IngestPreview,
  Job,
  JobEvent,
  NotificationSettings,
  StatusResponse,
  User,
} from './types'

type Route = 'dashboard' | 'ingest' | 'downloads' | 'helpers' | 'jobs' | 'settings'

type AuthState = {
  setupRequired: boolean
  user?: User
}

type GroupDraft = {
  name: string
  basePath: string
  newFolder: string
  linksText: string
  transcode: boolean
}

type IngestDraft = {
  helperId: string
  pathsText: string
  moveFiles: boolean
  verifyHash: boolean
  targetMode: 'camera' | 'project'
  basePath: string
  projectFolder: string
}

const routes: Array<{ key: Route; label: string }> = [
  { key: 'dashboard', label: 'Dashboard' },
  { key: 'ingest', label: 'Ingest' },
  { key: 'downloads', label: 'Downloads' },
  { key: 'helpers', label: 'Helpers' },
  { key: 'jobs', label: 'Jobs' },
  { key: 'settings', label: 'Settings / Audit' },
]

const DEFAULT_NAS_ROOT = '/mnt/Main/AIDEN'
const INGEST_DRAFT_KEY = 'roughdash.ingest.draft'
const DOWNLOADS_DRAFT_KEY = 'roughdash.downloads.draft'

function App() {
  const [auth, setAuth] = useState<AuthState | null>(null)
  const [route, setRoute] = useState<Route>(routeFromPath(window.location.pathname))
  const [toast, setToast] = useState('')
  const activeRoute = routes.find((entry) => entry.key === route) ?? routes[0]

  useEffect(() => {
    void refreshStatus()
  }, [])

  useEffect(() => {
    const onPopState = () => setRoute(routeFromPath(window.location.pathname))
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])

  async function refreshStatus() {
    try {
      const status = await api<StatusResponse>('/api/status')
      setAuth({ setupRequired: status.setupRequired, user: status.user })
    } catch (error) {
      setToast((error as Error).message)
    }
  }

  function navigate(nextRoute: Route) {
    window.history.pushState({}, '', routeToPath(nextRoute))
    setRoute(nextRoute)
  }

  if (!auth) {
    return <Splash message="Booting roughdash control plane..." />
  }

  if (!auth.user) {
    return (
      <AuthScreen
        setupRequired={auth.setupRequired}
        onAuthed={refreshStatus}
        onToast={setToast}
      />
    )
  }

  return (
    <div className="desktop">
      <div className="shell">
        <aside className="window window--sidebar window--sidebar-nav">
          <div className="window__body sidebar">
            <div className="brand">
              <div className="brand__title">roughdash</div>
              <div className="brand__subtitle">media desk for {auth.user.username}</div>
            </div>
            <nav className="nav">
              {routes.map((entry) => (
                <button
                  key={entry.key}
                  type="button"
                  className={`nav__item ${route === entry.key ? 'is-active' : ''}`}
                  onClick={() => navigate(entry.key)}
                >
                  <span>{entry.label}</span>
                </button>
              ))}
            </nav>
            <div className="sidebar__footer">
              <div className="pill">Signed in as {auth.user.username}</div>
              <button
                className="ghost-button"
                type="button"
                onClick={async () => {
                  await api<Record<string, never>>('/api/logout', { method: 'POST' })
                  setAuth({ setupRequired: false })
                  setToast('')
                }}
              >
                Logout
              </button>
            </div>
          </div>
        </aside>
        <main className="window window--main">
          <div className="window__titlebar">
            <div className="titlebar-controls">
              <span />
              <span />
            </div>
            <span>{activeRoute.label}</span>
          </div>
          <div className="window__body content">
            {toast ? <div className="toast">{toast}</div> : null}
            {route === 'dashboard' ? <DashboardPage onToast={setToast} /> : null}
            {route === 'ingest' ? <IngestPage onToast={setToast} /> : null}
            {route === 'downloads' ? <DownloadsPage onToast={setToast} /> : null}
            {route === 'helpers' ? <HelpersPage onToast={setToast} /> : null}
            {route === 'jobs' ? <JobsPage onToast={setToast} /> : null}
            {route === 'settings' ? <SettingsPage onToast={setToast} /> : null}
          </div>
        </main>
      </div>
    </div>
  )
}

function Splash({ message }: { message: string }) {
  return (
    <div className="desktop desktop--auth">
      <div className="auth-shell">
        <div className="window auth-card">
          <div className="window__titlebar">
            <div className="titlebar-controls">
              <span />
              <span />
            </div>
            <span>Launching roughdash</span>
          </div>
          <div className="window__body auth-card__body">
            <h1>{message}</h1>
          </div>
        </div>
      </div>
    </div>
  )
}

function AuthScreen({
  setupRequired,
  onAuthed,
  onToast,
}: {
  setupRequired: boolean
  onAuthed: () => Promise<void>
  onToast: (message: string) => void
}) {
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [bootstrapSecret, setBootstrapSecret] = useState('roughdash-bootstrap')
  const [busy, setBusy] = useState(false)

  async function runSetup(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    try {
      await api<{ user: User }>('/api/setup', {
        method: 'POST',
        body: JSON.stringify({ bootstrapSecret, username, password }),
      })
      await onAuthed()
      onToast('Setup complete.')
    } catch (error) {
      onToast((error as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function runLogin(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    try {
      await api<{ user: User }>('/api/login', {
        method: 'POST',
        body: JSON.stringify({ username, password }),
      })
      await onAuthed()
    } catch (error) {
      onToast((error as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="desktop desktop--auth">
      <div className="auth-shell">
        <div className="window auth-card auth-card--wide">
          <div className="window__titlebar">
            <div className="titlebar-controls">
              <span />
              <span />
            </div>
            <span>{setupRequired ? 'Welcome to roughdash' : 'Authenticate roughdash'}</span>
          </div>
          <div className="window__body auth-card__body">
            <div className="auth-intro">
              <h1>Single-user media operations desk</h1>
              <p>
                Bootstrap the admin account, then sign into the queue and helper control
                surface.
              </p>
            </div>
            <div className="auth-grid">
              {setupRequired ? (
                <section className="panel">
                  <h2>Bootstrap</h2>
                  <form className="stack" onSubmit={runSetup}>
                    <label>
                      <span>Bootstrap secret</span>
                      <input value={bootstrapSecret} onChange={(event) => setBootstrapSecret(event.target.value)} />
                    </label>
                    <label>
                      <span>Admin username</span>
                      <input value={username} onChange={(event) => setUsername(event.target.value)} />
                    </label>
                    <label>
                      <span>Password</span>
                      <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
                    </label>
                    <button className="primary-button" type="submit" disabled={busy}>
                      Initialize roughdash
                    </button>
                  </form>
                </section>
              ) : null}
              <section className="panel">
                <h2>Sign in</h2>
                <form className="stack" onSubmit={runLogin}>
                  <label>
                    <span>Username</span>
                    <input value={username} onChange={(event) => setUsername(event.target.value)} />
                  </label>
                  <label>
                    <span>Password</span>
                    <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
                  </label>
                  <button className="primary-button" type="submit" disabled={busy}>
                    Continue
                  </button>
                </form>
              </section>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

function DashboardPage({ onToast }: { onToast: (message: string) => void }) {
  const [data, setData] = useState<DashboardResponse | null>(null)

  useEffect(() => {
    void api<DashboardResponse>('/api/dashboard')
      .then(setData)
      .catch((error: Error) => onToast(error.message))
  }, [onToast])

  if (!data) {
    return <Page title="Dashboard" subtitle="Loading cluster state..." />
  }

  const helpers = data.helpers ?? []

  return (
    <Page title="Dashboard" subtitle="Single-user queue and helper overview">
      <div className="card-grid">
        <StatCard label="Active jobs" value={String(data.activeJobs)} />
        <StatCard label="Total jobs" value={String(data.totalJobs)} />
        <StatCard label="Helpers online" value={String(helpers.filter((item) => item.online).length)} />
        <StatCard label="NAS root" value={data.nasRoot} wide />
      </div>
      <div className="two-column">
        <section className="panel">
          <h2>Connected machines</h2>
          <div className="table">
            {helpers.map((helper) => (
              <div key={helper.id} className="table__row">
                <span>{helper.name}</span>
                <span>{helper.platform}</span>
                <span className={`status ${helper.online ? 'is-online' : 'is-offline'}`}>
                  {helper.online ? 'online' : 'offline'}
                </span>
              </div>
            ))}
          </div>
        </section>
        <section className="panel">
          <h2>Workflow notes</h2>
          <div className="terminal-block">
            <div>Ingest supports photos, videos, and matched .xml sidecars.</div>
            <div>Downloads run on the NAS and can create folders inline at submission time.</div>
            <div>Helper browsing is wired. Remote helper file transfer is reserved for the next backend pass.</div>
          </div>
        </section>
      </div>
    </Page>
  )
}

function IngestPage({ onToast }: { onToast: (message: string) => void }) {
  const initialDraft = loadPersistent<IngestDraft>(INGEST_DRAFT_KEY, defaultIngestDraft())
  const [helperId, setHelperId] = useState(initialDraft.helperId)
  const [helpers, setHelpers] = useState<Helper[]>([])
  const [pathsText, setPathsText] = useState(initialDraft.pathsText)
  const [moveFiles, setMoveFiles] = useState(initialDraft.moveFiles)
  const [verifyHash, setVerifyHash] = useState(initialDraft.verifyHash)
  const [targetMode, setTargetMode] = useState<'camera' | 'project'>(initialDraft.targetMode)
  const [basePath, setBasePath] = useState(initialDraft.basePath)
  const [projectFolder, setProjectFolder] = useState(initialDraft.projectFolder)
  const [preview, setPreview] = useState<IngestPreview | null>(null)
  const [browsePath, setBrowsePath] = useState('/')
  const [entries, setEntries] = useState<FileEntry[]>([])
  const [browseMode, setBrowseMode] = useState<'source' | 'target'>('source')

  useEffect(() => {
    void api<{ helpers: Helper[] }>('/api/helpers')
      .then((response) => {
        setHelpers(response.helpers)
        if (!response.helpers.some((helper) => helper.id === helperId)) {
          setHelperId('local')
        }
      })
      .catch((error: Error) => onToast(error.message))
  }, [helperId, onToast])

  useEffect(() => {
    savePersistent(INGEST_DRAFT_KEY, {
      helperId,
      pathsText,
      moveFiles,
      verifyHash,
      targetMode,
      basePath,
      projectFolder,
    } satisfies IngestDraft)
  }, [helperId, pathsText, moveFiles, verifyHash, targetMode, basePath, projectFolder])

  useEffect(() => {
    const targetHelper = browseMode === 'target' ? 'local' : helperId
    const path = browseMode === 'target' ? normalizeFolderPath(browsePath === '/' ? DEFAULT_NAS_ROOT : browsePath) : browsePath
    void api<{ entries: FileEntry[] }>(`/api/helpers/${targetHelper}/browse`, {
      method: 'POST',
      body: JSON.stringify({ path, mode: browseMode }),
    })
      .then((response) => setEntries(response.entries))
      .catch((error: Error) => onToast(error.message))
  }, [browsePath, browseMode, helperId, onToast])

  async function previewJob() {
    try {
      const response = await api<IngestPreview>('/api/ingest/preview', {
        method: 'POST',
        body: JSON.stringify({
          sourceType: helperId === 'local' ? 'local' : 'helper',
          helperId,
          paths: lines(pathsText),
          moveFiles,
          verifyHash,
          target: {
            mode: targetMode,
            basePath,
            projectFolder,
          },
        }),
      })
      setPreview(response)
    } catch (error) {
      onToast((error as Error).message)
    }
  }

  async function createJob() {
    try {
      await api<Record<string, never>>('/api/ingest/jobs', {
        method: 'POST',
        body: JSON.stringify({
          sourceType: helperId === 'local' ? 'local' : 'helper',
          helperId,
          paths: lines(pathsText),
          moveFiles,
          verifyHash,
          target: {
            mode: targetMode,
            basePath,
            projectFolder,
          },
        }),
      })
      setPreview(null)
      onToast('Ingest job queued.')
    } catch (error) {
      onToast((error as Error).message)
    }
  }

  function resetDraft() {
    const draft = defaultIngestDraft()
    setHelperId(draft.helperId)
    setPathsText(draft.pathsText)
    setMoveFiles(draft.moveFiles)
    setVerifyHash(draft.verifyHash)
    setTargetMode(draft.targetMode)
    setBasePath(draft.basePath)
    setProjectFolder(draft.projectFolder)
    setPreview(null)
    clearPersistent(INGEST_DRAFT_KEY)
  }

  return (
    <Page title="Ingest" subtitle="Choose a source machine, target mode, and verify the final file map before queueing.">
      <div className="two-column">
        <section className="panel">
          <h2>Job input</h2>
          <div className="stack">
            <label>
              <span>Source helper</span>
              <select value={helperId} onChange={(event) => setHelperId(event.target.value)}>
                {helpers.map((helper) => (
                  <option key={helper.id} value={helper.id}>
                    {helper.name}
                  </option>
                ))}
              </select>
            </label>
            <div className="inline-toggle">
              <button
                type="button"
                className={targetMode === 'camera' ? 'chip chip--active' : 'chip'}
                onClick={() => setTargetMode('camera')}
              >
                Camera library
              </button>
              <button
                type="button"
                className={targetMode === 'project' ? 'chip chip--active' : 'chip'}
                onClick={() => setTargetMode('project')}
              >
                Project folder
              </button>
            </div>
            {targetMode === 'project' ? (
              <>
                <FolderPicker label="Base path" value={basePath} onChange={setBasePath} />
                <label>
                  <span>Project folder</span>
                  <input value={projectFolder} onChange={(event) => setProjectFolder(event.target.value)} placeholder="Poland / Australia / client-name" />
                </label>
              </>
            ) : null}
            <label>
              <span>Selected source paths</span>
              <textarea
                rows={7}
                value={pathsText}
                onChange={(event) => setPathsText(event.target.value)}
                placeholder="/Volumes/CARD_A/CLIPS&#10;/Volumes/CARD_A/VOICE"
              />
            </label>
            <label className="checkbox">
              <input type="checkbox" checked={moveFiles} onChange={(event) => setMoveFiles(event.target.checked)} />
              <span>Move files after successful ingest</span>
            </label>
            <label className="checkbox">
              <input type="checkbox" checked={verifyHash} onChange={(event) => setVerifyHash(event.target.checked)} />
              <span>Verify checksums after copy</span>
            </label>
            <div className="row-actions">
              <button className="ghost-button" type="button" onClick={resetDraft}>
                Reset
              </button>
              <button className="secondary-button" type="button" onClick={previewJob}>
                Preview ingest
              </button>
              <button className="primary-button" type="button" onClick={createJob}>
                Queue ingest
              </button>
            </div>
            {helperId !== 'local' ? (
              <div className="callout">
                Remote helper browse is live. Remote helper file transfer is reserved for the next backend pass, so job creation currently rejects non-local helpers.
              </div>
            ) : null}
          </div>
        </section>
        <section className="panel">
          <h2>Browser</h2>
          <div className="inline-toggle">
            <button
              type="button"
              className={browseMode === 'source' ? 'chip chip--active' : 'chip'}
              onClick={() => {
                setBrowseMode('source')
                setBrowsePath('/')
              }}
            >
              Source
            </button>
            <button
              type="button"
              className={browseMode === 'target' ? 'chip chip--active' : 'chip'}
              onClick={() => {
                setBrowseMode('target')
                setBrowsePath(basePath || DEFAULT_NAS_ROOT)
              }}
            >
              Target
            </button>
          </div>
          <label>
            <span>Browse path</span>
            <input value={browsePath} onChange={(event) => setBrowsePath(event.target.value)} />
          </label>
          <div className="browser-list">
            {entries.map((entry) => (
              <button
                key={entry.path}
                className="browser-item"
                type="button"
                onClick={() => {
                  if (entry.isDir) {
                    setBrowsePath(entry.path)
                    return
                  }
                  if (browseMode === 'source') {
                    setPathsText((current) => appendLine(current, entry.path))
                  } else {
                    setBasePath(entry.path)
                  }
                }}
              >
                <span>{entry.isDir ? '[DIR]' : '[FILE]'}</span>
                <span>{entry.path}</span>
              </button>
            ))}
          </div>
        </section>
      </div>
      {preview ? (
        <section className="panel">
          <h2>Preview</h2>
          <div className="terminal-block">
            <div>Target root: {preview.targetPath}</div>
            <div>Files: {preview.files.length}</div>
            <div>Estimated size: {formatBytes(preview.estimatedSize)}</div>
          </div>
          <div className="table">
            {preview.files.slice(0, 20).map((item) => (
              <div key={`${item.sourcePath}-${item.destinationPath}`} className="table__row table__row--stack">
                <span>{item.kind}</span>
                <span>{item.sourcePath}</span>
                <span>{item.destinationPath}</span>
              </div>
            ))}
          </div>
        </section>
      ) : null}
    </Page>
  )
}

function DownloadsPage({ onToast }: { onToast: (message: string) => void }) {
  const [groups, setGroups] = useState<GroupDraft[]>(() =>
    loadPersistent<GroupDraft[]>(DOWNLOADS_DRAFT_KEY, defaultDownloadGroups()),
  )
  const [preview, setPreview] = useState<DownloadPreview | null>(null)

  useEffect(() => {
    savePersistent(DOWNLOADS_DRAFT_KEY, groups)
  }, [groups])

  function updateGroup(index: number, patch: Partial<GroupDraft>) {
    setGroups((current) => current.map((group, currentIndex) => (currentIndex === index ? { ...group, ...patch } : group)))
  }

  async function previewJob() {
    try {
      const response = await api<DownloadPreview>('/api/downloads/preview', {
        method: 'POST',
        body: JSON.stringify({
          groups: groups.map((group) => ({
            name: group.name,
            basePath: group.basePath,
            newFolder: group.newFolder,
            links: lines(group.linksText),
            transcode: group.transcode,
          })),
        }),
      })
      setPreview(response)
    } catch (error) {
      onToast((error as Error).message)
    }
  }

  async function createJob() {
    try {
      await api<Record<string, never>>('/api/downloads/jobs', {
        method: 'POST',
        body: JSON.stringify({
          groups: groups.map((group) => ({
            name: group.name,
            basePath: group.basePath,
            newFolder: group.newFolder,
            links: lines(group.linksText),
            transcode: group.transcode,
          })),
        }),
      })
      setPreview(null)
      onToast('Download job queued.')
    } catch (error) {
      onToast((error as Error).message)
    }
  }

  function resetDraft() {
    setGroups(defaultDownloadGroups())
    setPreview(null)
    clearPersistent(DOWNLOADS_DRAFT_KEY)
  }

  return (
    <Page title="Downloads" subtitle="Build one or more group folders, preview final filenames, then queue yt-dlp and transcode work on the NAS.">
      <section className="panel">
        <div className="row-space">
          <h2>Download groups</h2>
          <button
            className="secondary-button"
            type="button"
            onClick={() =>
              setGroups((current) => [
                ...current,
                { name: '', basePath: DEFAULT_NAS_ROOT, newFolder: '', linksText: '', transcode: true },
              ])
            }
          >
            Add group
          </button>
        </div>
        <div className="stack">
          {groups.map((group, index) => (
            <div key={`${group.name}-${index}`} className="group-card">
              <label>
                <span>Group label</span>
                <input value={group.name} onChange={(event) => updateGroup(index, { name: event.target.value })} />
              </label>
              <FolderPicker
                label="Base path"
                value={group.basePath}
                onChange={(value) => updateGroup(index, { basePath: value })}
              />
              <label>
                <span>New folder (optional)</span>
                <input value={group.newFolder} onChange={(event) => updateGroup(index, { newFolder: event.target.value })} />
              </label>
              <label>
                <span>Links</span>
                <textarea
                  rows={6}
                  value={group.linksText}
                  onChange={(event) => updateGroup(index, { linksText: event.target.value })}
                  placeholder="https://youtube.com/watch?v=...&#10;https://youtube.com/playlist?list=..."
                />
              </label>
              <label className="checkbox">
                <input type="checkbox" checked={group.transcode} onChange={(event) => updateGroup(index, { transcode: event.target.checked })} />
                <span>Archive HEVC MP4 after download</span>
              </label>
            </div>
          ))}
          <div className="row-actions">
            <button className="ghost-button" type="button" onClick={resetDraft}>
              Reset
            </button>
            <button className="secondary-button" type="button" onClick={previewJob}>
              Preview downloads
            </button>
            <button className="primary-button" type="button" onClick={createJob}>
              Queue downloads
            </button>
          </div>
        </div>
      </section>
      {preview ? (
        <section className="panel">
          <h2>Preview</h2>
          {preview.preview.groups.map((group) => (
            <div key={group.name} className="preview-group">
              <div className="terminal-block">
                <div>Group: {group.name}</div>
                <div>Target: {group.targetPath}</div>
                <div>Videos: {group.videos.length}</div>
              </div>
              <div className="table">
                {group.videos.map((video) => (
                  <div key={`${video.link}-${video.videoId}`} className="table__row table__row--stack">
                    <span>{video.uploader}</span>
                    <span>{video.title}</span>
                    <span>{video.finalPath}</span>
                  </div>
                ))}
              </div>
            </div>
          ))}
        </section>
      ) : null}
    </Page>
  )
}

function HelpersPage({ onToast }: { onToast: (message: string) => void }) {
  const [helpers, setHelpers] = useState<Helper[]>([])
  const [pairCode, setPairCode] = useState('')
  const [browseHelper, setBrowseHelper] = useState('local')
  const [browsePath, setBrowsePath] = useState('/')
  const [entries, setEntries] = useState<FileEntry[]>([])

  async function refresh() {
    try {
      const response = await api<{ helpers: Helper[] }>('/api/helpers')
      setHelpers(response.helpers)
      if (!response.helpers.some((helper) => helper.id === browseHelper)) {
        setBrowseHelper(response.helpers[0]?.id ?? 'local')
      }
    } catch (error) {
      onToast((error as Error).message)
    }
  }

  useEffect(() => {
    void refresh()
  }, [])

  useEffect(() => {
    void api<{ entries: FileEntry[] }>(`/api/helpers/${browseHelper}/browse`, {
      method: 'POST',
      body: JSON.stringify({ path: browsePath, mode: 'source' }),
    })
      .then((response) => setEntries(response.entries))
      .catch((error: Error) => onToast(error.message))
  }, [browseHelper, browsePath, onToast])

  return (
    <Page title="Helpers" subtitle="Approve one-time codes, inspect online status, and browse connected machines.">
      <div className="two-column">
        <section className="panel">
          <h2>Connected helpers</h2>
          <div className="table">
            {helpers.map((helper) => (
              <div key={helper.id} className="table__row">
                <span>{helper.name}</span>
                <span>{helper.platform}</span>
                <span className={`status ${helper.online ? 'is-online' : 'is-offline'}`}>{helper.online ? 'online' : 'offline'}</span>
                {helper.id !== 'local' ? (
                  <button
                    className="ghost-button"
                    type="button"
                    onClick={async () => {
                      try {
                        await api<Record<string, never>>(`/api/helpers/${helper.id}/revoke`, { method: 'POST' })
                        await refresh()
                      } catch (error) {
                        onToast((error as Error).message)
                      }
                    }}
                  >
                    Revoke
                  </button>
                ) : null}
              </div>
            ))}
          </div>
          <form
            className="stack"
            onSubmit={async (event) => {
              event.preventDefault()
              try {
                await api<Record<string, never>>('/api/helpers/pair', {
                  method: 'POST',
                  body: JSON.stringify({ code: pairCode }),
                })
                setPairCode('')
                await refresh()
              } catch (error) {
                onToast((error as Error).message)
              }
            }}
          >
            <label>
              <span>Pairing code from helper machine</span>
              <input value={pairCode} onChange={(event) => setPairCode(event.target.value)} placeholder="123456" />
            </label>
            <button className="primary-button" type="submit">Approve helper</button>
          </form>
        </section>
        <section className="panel">
          <h2>Remote browser</h2>
          <label>
            <span>Helper</span>
            <select value={browseHelper} onChange={(event) => setBrowseHelper(event.target.value)}>
              {helpers.map((helper) => (
                <option key={helper.id} value={helper.id}>
                  {helper.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>Path</span>
            <input value={browsePath} onChange={(event) => setBrowsePath(event.target.value)} />
          </label>
          <div className="browser-list">
            {entries.map((entry) => (
              <button key={entry.path} className="browser-item" type="button" onClick={() => entry.isDir && setBrowsePath(entry.path)}>
                <span>{entry.isDir ? '[DIR]' : '[FILE]'}</span>
                <span>{entry.path}</span>
              </button>
            ))}
          </div>
        </section>
      </div>
    </Page>
  )
}

function JobsPage({ onToast }: { onToast: (message: string) => void }) {
  const [jobs, setJobs] = useState<Job[]>([])
  const [selectedJobId, setSelectedJobId] = useState('')
  const [events, setEvents] = useState<JobEvent[]>([])

  async function refreshJobs() {
    try {
      const response = await api<{ jobs: Job[] }>('/api/jobs')
      setJobs(response.jobs)
      if (!selectedJobId && response.jobs[0]) {
        setSelectedJobId(response.jobs[0].id)
      }
    } catch (error) {
      onToast((error as Error).message)
    }
  }

  async function refreshSelectedJob(jobId: string) {
    try {
      const response = await api<{ job: Job; events: JobEvent[] }>(`/api/jobs/${jobId}`)
      setEvents(response.events)
    } catch (error) {
      onToast((error as Error).message)
    }
  }

  useEffect(() => {
    void refreshJobs()
    const timer = window.setInterval(() => void refreshJobs(), 5000)
    const eventSource = new EventSource(`${import.meta.env.VITE_API_BASE ?? ''}/api/events`, { withCredentials: true })
    eventSource.onmessage = () => {
      void refreshJobs()
      if (selectedJobId) {
        void refreshSelectedJob(selectedJobId)
      }
    }
    return () => {
      window.clearInterval(timer)
      eventSource.close()
    }
  }, [])

  useEffect(() => {
    if (selectedJobId) {
      void refreshSelectedJob(selectedJobId)
    }
  }, [selectedJobId])

  const selectedJob = useMemo(() => jobs.find((item) => item.id === selectedJobId), [jobs, selectedJobId])

  return (
    <Page title="Jobs" subtitle="Queue state, progress, and detailed event logs.">
      <div className="two-column">
        <section className="panel">
          <h2>Queue</h2>
          <div className="table">
            {jobs.map((job) => (
              <button key={job.id} className={`table__row table__row--job ${job.id === selectedJobId ? 'is-selected' : ''}`} type="button" onClick={() => setSelectedJobId(job.id)}>
                <span>{job.summary}</span>
                <span>{job.status}</span>
                <span>{Math.round(job.progress * 100)}%</span>
              </button>
            ))}
          </div>
        </section>
        <section className="panel">
          <h2>Detail</h2>
          {selectedJob ? (
            <>
              <div className="terminal-block">
                <div>ID: {selectedJob.id}</div>
                <div>Status: {selectedJob.status}</div>
                <div>Progress: {Math.round(selectedJob.progress * 100)}%</div>
                {selectedJob.error ? <div>Error: {selectedJob.error}</div> : null}
              </div>
              <div className="row-actions">
                <JobActionButton id={selectedJob.id} action="pause" label="Pause" onToast={onToast} onDone={() => void refreshJobs()} />
                <JobActionButton id={selectedJob.id} action="resume" label="Resume / Retry" onToast={onToast} onDone={() => void refreshJobs()} />
                <JobActionButton id={selectedJob.id} action="cancel" label="Cancel" onToast={onToast} onDone={() => void refreshJobs()} />
              </div>
              <div className="event-log">
                {events.map((event) => (
                  <div key={event.id} className="event-log__item">
                    <span>{event.createdAt}</span>
                    <span>{event.level}</span>
                    <span>{event.message}</span>
                  </div>
                ))}
              </div>
            </>
          ) : (
            <div className="terminal-block">No job selected.</div>
          )}
        </section>
      </div>
    </Page>
  )
}

function SettingsPage({ onToast }: { onToast: (message: string) => void }) {
  const [settings, setSettings] = useState<NotificationSettings>({
    browserEnabled: true,
    telegramEnabled: false,
    telegramBotToken: '',
    telegramChatId: '',
  })
  const [audit, setAudit] = useState<AuditEntry[]>([])

  async function refresh() {
    try {
      const [settingsResponse, auditResponse] = await Promise.all([
        api<NotificationSettings>('/api/settings'),
        api<{ entries: AuditEntry[] }>('/api/audit'),
      ])
      setSettings(settingsResponse)
      setAudit(auditResponse.entries)
    } catch (error) {
      onToast((error as Error).message)
    }
  }

  useEffect(() => {
    void refresh()
  }, [])

  return (
    <Page title="Settings / Audit" subtitle="Global notifications and the operator audit trail.">
      <div className="two-column">
        <section className="panel">
          <h2>Notifications</h2>
          <form
            className="stack"
            onSubmit={async (event) => {
              event.preventDefault()
              try {
                await api<NotificationSettings>('/api/settings', { method: 'PUT', body: JSON.stringify(settings) })
                onToast('Settings saved.')
              } catch (error) {
                onToast((error as Error).message)
              }
            }}
          >
            <label className="checkbox">
              <input type="checkbox" checked={settings.browserEnabled} onChange={(event) => setSettings({ ...settings, browserEnabled: event.target.checked })} />
              <span>Browser notifications</span>
            </label>
            <label className="checkbox">
              <input type="checkbox" checked={settings.telegramEnabled} onChange={(event) => setSettings({ ...settings, telegramEnabled: event.target.checked })} />
              <span>Telegram notifications</span>
            </label>
            <label>
              <span>Telegram bot token</span>
              <input value={settings.telegramBotToken} onChange={(event) => setSettings({ ...settings, telegramBotToken: event.target.value })} />
            </label>
            <label>
              <span>Telegram chat ID</span>
              <input value={settings.telegramChatId} onChange={(event) => setSettings({ ...settings, telegramChatId: event.target.value })} />
            </label>
            <div className="row-actions">
              <button className="primary-button" type="submit">Save settings</button>
              <button
                className="secondary-button"
                type="button"
                onClick={async () => {
                  const response = await fetch(`${import.meta.env.VITE_API_BASE ?? ''}/api/export`, { method: 'POST', credentials: 'include' })
                  const blob = await response.blob()
                  const url = window.URL.createObjectURL(blob)
                  const anchor = document.createElement('a')
                  anchor.href = url
                  anchor.download = 'roughdash-export.json'
                  anchor.click()
                  window.URL.revokeObjectURL(url)
                }}
              >
                Export snapshot
              </button>
            </div>
          </form>
        </section>
        <section className="panel">
          <h2>Audit trail</h2>
          <div className="event-log">
            {audit.map((entry) => (
              <div key={entry.id} className="event-log__item">
                <span>{entry.createdAt}</span>
                <span>{entry.actor}</span>
                <span>{entry.action}</span>
                <span>{entry.target}</span>
              </div>
            ))}
          </div>
        </section>
      </div>
    </Page>
  )
}

function Page({
  title,
  subtitle,
  children,
}: {
  title: string
  subtitle: string
  children?: ReactNode
}) {
  return (
    <div className="page">
      <header className="page__header">
        <div>
          <h1>{title}</h1>
        </div>
        <p>{subtitle}</p>
      </header>
      {children}
    </div>
  )
}

function FolderPicker({
  label,
  value,
  onChange,
  rootPath = DEFAULT_NAS_ROOT,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  rootPath?: string
}) {
  const [open, setOpen] = useState(false)
  const [browsePath, setBrowsePath] = useState(value || rootPath)
  const [entries, setEntries] = useState<FileEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) {
      return
    }
    const path = normalizeFolderPath(browsePath || value || rootPath)
    let cancelled = false
    setLoading(true)
    void api<{ entries: FileEntry[] }>('/api/helpers/local/browse', {
      method: 'POST',
      body: JSON.stringify({ path, mode: 'target' }),
    })
      .then((response) => {
        if (cancelled) {
          return
        }
        setEntries(response.entries.filter((entry) => entry.isDir))
        setError('')
      })
      .catch((requestError: Error) => {
        if (cancelled) {
          return
        }
        setEntries([])
        setError(requestError.message)
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false)
        }
      })
    return () => {
      cancelled = true
    }
  }, [browsePath, open, rootPath, value])

  const currentPath = normalizeFolderPath(open ? browsePath : value || rootPath)
  const canGoUp = currentPath !== normalizeFolderPath(rootPath)

  return (
    <div className="folder-picker">
      <span>{label}</span>
      <button
        type="button"
        className="folder-picker__trigger"
        onClick={() => {
          setBrowsePath(normalizeFolderPath(value || rootPath))
          setOpen((current) => !current)
        }}
      >
        <span className="folder-picker__value">{value || rootPath}</span>
        <span className="folder-picker__action">{open ? 'Close' : 'Choose folder'}</span>
      </button>
      {open ? (
        <div className="folder-picker__menu">
          <div className="folder-picker__toolbar">
            <button
              type="button"
              className="secondary-button"
              disabled={!canGoUp}
              onClick={() => setBrowsePath(parentDirectory(currentPath))}
            >
              Up
            </button>
            <button
              type="button"
              className="primary-button"
              onClick={() => {
                onChange(currentPath)
                setOpen(false)
              }}
            >
              Use this folder
            </button>
          </div>
          <div className="folder-picker__path">{currentPath}</div>
          {error ? <div className="callout">{error}</div> : null}
          <div className="browser-list">
            {loading ? <div className="terminal-block">Loading folders…</div> : null}
            {!loading && entries.length === 0 ? (
              <div className="terminal-block">No subfolders here.</div>
            ) : null}
            {!loading
              ? entries.map((entry) => (
                  <button
                    key={entry.path}
                    className="browser-item"
                    type="button"
                    onClick={() => setBrowsePath(entry.path)}
                  >
                    <span>{entry.name}</span>
                    <span>{entry.path}</span>
                  </button>
                ))
              : null}
          </div>
        </div>
      ) : null}
    </div>
  )
}

function StatCard({ label, value, wide = false }: { label: string; value: string; wide?: boolean }) {
  return (
    <div className={`stat-card ${wide ? 'stat-card--wide' : ''}`}>
      <div className="stat-card__label">{label}</div>
      <div className="stat-card__value">{value}</div>
    </div>
  )
}

function JobActionButton({
  id,
  action,
  label,
  onToast,
  onDone,
}: {
  id: string
  action: 'pause' | 'resume' | 'cancel'
  label: string
  onToast: (message: string) => void
  onDone: () => void
}) {
  return (
    <button
      className="ghost-button"
      type="button"
      onClick={async () => {
        try {
          await api<Record<string, never>>(`/api/jobs/${id}/${action}`, { method: 'POST' })
          onDone()
        } catch (error) {
          onToast((error as Error).message)
        }
      }}
    >
      {label}
    </button>
  )
}

function routeFromPath(pathname: string): Route {
  const key = pathname.replace(/^\//, '') as Route
  return routes.some((route) => route.key === key) ? key : 'dashboard'
}

function routeToPath(route: Route) {
  return route === 'dashboard' ? '/' : `/${route}`
}

function appendLine(current: string, value: string) {
  return current.trim() ? `${current}\n${value}` : value
}

function defaultIngestDraft(): IngestDraft {
  return {
    helperId: 'local',
    pathsText: '',
    moveFiles: false,
    verifyHash: false,
    targetMode: 'camera',
    basePath: DEFAULT_NAS_ROOT,
    projectFolder: '',
  }
}

function defaultDownloadGroups(): GroupDraft[] {
  return [
    {
      name: 'Poland',
      basePath: DEFAULT_NAS_ROOT,
      newFolder: 'Poland',
      linksText: '',
      transcode: true,
    },
  ]
}

function normalizeFolderPath(value: string) {
  const normalized = value.trim().replace(/\/+$/, '')
  return normalized || '/'
}

function parentDirectory(value: string) {
  const normalized = normalizeFolderPath(value)
  if (normalized === '/') {
    return '/'
  }
  const lastSlash = normalized.lastIndexOf('/')
  if (lastSlash <= 0) {
    return '/'
  }
  return normalized.slice(0, lastSlash)
}

function lines(value: string) {
  return value.split('\n').map((line) => line.trim()).filter(Boolean)
}

function formatBytes(value: number) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit += 1
  }
  return `${amount.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

function loadPersistent<T>(key: string, fallback: T): T {
  if (typeof window === 'undefined') {
    return fallback
  }
  try {
    const raw = window.localStorage.getItem(key)
    if (!raw) {
      return fallback
    }
    return JSON.parse(raw) as T
  } catch {
    return fallback
  }
}

function savePersistent(key: string, value: unknown) {
  if (typeof window === 'undefined') {
    return
  }
  window.localStorage.setItem(key, JSON.stringify(value))
}

function clearPersistent(key: string) {
  if (typeof window === 'undefined') {
    return
  }
  window.localStorage.removeItem(key)
}

export default App
