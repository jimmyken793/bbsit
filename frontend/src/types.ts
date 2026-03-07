export type ConfigMode = 'form' | 'custom'
export type HealthType = 'http' | 'tcp' | 'none'
export type ProjectStatus = 'unknown' | 'running' | 'stopped' | 'deploying' | 'failed' | 'rolled_back'
export type DeployStatus = 'success' | 'failed' | 'rolled_back' | 'in_progress'
export type DeployTrigger = 'poll' | 'manual' | 'startup'

export interface PortMapping {
  host_port: number
  container_port: number
  protocol?: string
}

export interface VolumeMount {
  host_path: string
  container_path: string
  readonly?: boolean
}

export interface Project {
  id: string
  display_name: string
  config_mode: ConfigMode
  registry_image?: string
  image_tag?: string
  ports?: PortMapping[]
  volumes?: VolumeMount[]
  env_vars?: Record<string, string>
  extra_options?: string
  bind_host?: string
  custom_compose?: string
  stack_path: string
  health_type: HealthType
  health_target: string
  poll_interval: number
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface ProjectState {
  project_id: string
  current_digest: string
  previous_digest: string
  desired_digest: string
  status: ProjectStatus
  last_check_at?: string
  last_deploy_at?: string
  last_success_at?: string
  last_error: string
}

export interface ProjectWithState extends Project {
  state: ProjectState
}

export interface Deployment {
  id: number
  project_id: string
  from_digest: string
  to_digest: string
  status: DeployStatus
  trigger: DeployTrigger
  started_at: string
  ended_at?: string
  error_message: string
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
