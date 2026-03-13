import yaml from 'js-yaml'
import type { ServiceConfig } from './types'

interface StackConfigYaml {
  services?: Array<{
    name: string
    registry_image: string
    image_tag: string
    polled: boolean
    ports?: Array<{ host_port: number; container_port: number; protocol?: string }>
    volumes?: Array<{ host_path: string; container_path: string; readonly?: boolean }>
    extra_options?: string
  }>
  registry_image?: string
  image_tag?: string
  ports?: Array<{ host_port: number; container_port: number; protocol?: string }>
  volumes?: Array<{ host_path: string; container_path: string; readonly?: boolean }>
  extra_options?: string
  env_vars?: Record<string, string>
}

export function formToYaml(
  services: ServiceConfig[],
  envPairs: [string, string][],
): string {
  const obj: StackConfigYaml = {}

  if (services.length > 0) {
    obj.services = services.map(svc => {
      const s: NonNullable<StackConfigYaml['services']>[number] = {
        name: svc.name,
        registry_image: svc.registry_image,
        image_tag: svc.image_tag || 'latest',
        polled: svc.polled,
      }
      if (svc.ports?.length) s.ports = svc.ports
      if (svc.volumes?.length) s.volumes = svc.volumes
      if (svc.extra_options) s.extra_options = svc.extra_options
      return s
    })
  }

  const envObj = Object.fromEntries(envPairs.filter(([k]) => k.trim()))
  if (Object.keys(envObj).length > 0) {
    obj.env_vars = envObj
  }

  return yaml.dump(obj, { lineWidth: -1, noRefs: true })
}

export function yamlToForm(text: string): {
  services: ServiceConfig[]
  envPairs: [string, string][]
} {
  const parsed = yaml.load(text) as StackConfigYaml | null
  if (!parsed || typeof parsed !== 'object') {
    return { services: [], envPairs: [] }
  }

  let services: ServiceConfig[]
  if (parsed.services && parsed.services.length > 0) {
    services = parsed.services.map(s => ({
      name: s.name || '',
      registry_image: s.registry_image || '',
      image_tag: s.image_tag || 'latest',
      polled: s.polled ?? true,
      ports: s.ports,
      volumes: s.volumes,
      extra_options: s.extra_options,
    }))
  } else if (parsed.registry_image) {
    services = [{
      name: '',
      registry_image: parsed.registry_image,
      image_tag: parsed.image_tag || 'latest',
      polled: true,
      ports: parsed.ports,
      volumes: parsed.volumes,
      extra_options: parsed.extra_options,
    }]
  } else {
    services = []
  }

  const envPairs: [string, string][] = parsed.env_vars
    ? Object.entries(parsed.env_vars)
    : []

  return { services, envPairs }
}
