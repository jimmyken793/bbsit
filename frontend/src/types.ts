export type ConfigMode = 'form' | 'custom'
export type HealthType = 'http' | 'tcp' | 'none'
export type ProjectStatus = 'unknown' | 'running' | 'stopped' | 'stopping' | 'starting' | 'deploying' | 'failed' | 'rolled_back'
export type DeployStatus = 'success' | 'failed' | 'rolled_back' | 'in_progress'
export type DeployTrigger = 'poll' | 'manual' | 'startup'

export interface PortMapping {
  host_port: string | number // e.g. 8080, "8080", "127.0.0.1:8080", "localhost:8080"
  container_port: number
  protocol?: string
}

export interface VolumeMount {
  host_path: string
  container_path: string
  readonly?: boolean
}

export interface PublicHostname {
  tunnel_id: string
  hostname: string
  port: number
}

export interface ServiceConfig {
  name: string
  registry_image: string
  image_tag: string
  polled: boolean
  platform?: string // e.g. linux/amd64, linux/arm64
  ports?: PortMapping[]
  volumes?: VolumeMount[]
  extra_options?: string
  health_type?: HealthType
  health_target?: string
  public_hostnames?: PublicHostname[]
}

export interface Project {
  id: string
  display_name: string
  config_mode: ConfigMode
  services?: ServiceConfig[]
  bind_host?: string
  // Legacy single-service fields (kept for backward compat)
  registry_image?: string
  image_tag?: string
  ports?: PortMapping[]
  volumes?: VolumeMount[]
  extra_options?: string
  custom_compose?: string
  stack_path: string
  health_type: HealthType
  health_target: string
  poll_interval: number
  enabled: boolean
  env_vars?: Record<string, string>
  is_system?: boolean
  created_at: string
  updated_at: string
}

export interface Tunnel {
  id: string
  name: string
  cf_tunnel_id: string
  account_tag: string
  enabled: boolean
  has_secret: boolean
  has_api_token: boolean
  created_at?: string
  updated_at?: string
}

export type TunnelRouteDNSStatus =
  | 'ok'
  | 'wrong_target'
  | 'not_proxied'
  | 'wrong_type'
  | 'missing'
  | 'no_zone'
  | 'no_token'
  | 'error'

export interface TunnelRoute {
  hostname: string
  project_id: string
  service: string
  port: number
  dns: {
    status: TunnelRouteDNSStatus
    expected_target?: string
    actual_target?: string
    actual_type?: string
    proxied?: boolean
    error?: string
  }
}

export interface TunnelInput {
  id?: string
  name?: string
  enabled?: boolean
  credentials?: string // raw credentials.json from CF dashboard
  cf_tunnel_id?: string
  account_tag?: string
  tunnel_secret?: string
  cf_api_token?: string // Zone.DNS:Edit + Zone.Zone:Read for auto-CNAME
}

export interface ProjectState {
  project_id: string
  current_digests: Record<string, string>
  previous_digests: Record<string, string>
  desired_digests: Record<string, string>
  status: ProjectStatus
  last_check_at?: string
  last_deploy_at?: string
  last_success_at?: string
  last_error: string
  // Legacy
  current_digest?: string
  previous_digest?: string
  desired_digest?: string
}

export interface ProjectWithState extends Project {
  state: ProjectState
}

export interface Deployment {
  id: number
  project_id: string
  from_digests: Record<string, string>
  to_digests: Record<string, string>
  status: DeployStatus
  trigger: DeployTrigger
  started_at: string
  ended_at?: string
  error_message: string
  // Legacy
  from_digest?: string
  to_digest?: string
}

export interface ProjectDetail {
  project: Project
  state: ProjectState
  deployments: Deployment[]
}

export interface AuthStatus {
  setup_required: boolean
  logged_in: boolean
}
