import { describe, it, expect } from 'vitest'
import { formToYaml, yamlToForm } from './stackYaml'
import type { ServiceConfig } from './types'

describe('formToYaml', () => {
  it('serializes multi-service with env vars', () => {
    const services: ServiceConfig[] = [
      { name: 'app', registry_image: 'reg/app', image_tag: 'latest', polled: true },
      { name: 'redis', registry_image: 'redis', image_tag: '7', polled: false },
    ]
    const envPairs: [string, string][] = [['DB_URL', 'postgres://localhost/db']]

    const yaml = formToYaml(services, envPairs)
    expect(yaml).toContain('name: app')
    expect(yaml).toContain('registry_image: reg/app')
    expect(yaml).toContain('name: redis')
    expect(yaml).toContain('image_tag: \'7\'')
    expect(yaml).toContain('DB_URL')
  })

  it('includes ports and volumes when present', () => {
    const services: ServiceConfig[] = [{
      name: 'app',
      registry_image: 'reg/app',
      image_tag: 'latest',
      polled: true,
      ports: [{ host_port: 8080, container_port: 80 }],
      volumes: [{ host_path: './data', container_path: '/app/data' }],
    }]

    const yaml = formToYaml(services, [])
    expect(yaml).toContain('host_port: 8080')
    expect(yaml).toContain('container_port: 80')
    expect(yaml).toContain("host_path: ./data")
    expect(yaml).toContain("container_path: /app/data")
  })

  it('omits empty env_vars', () => {
    const yaml = formToYaml(
      [{ name: 'app', registry_image: 'reg/app', image_tag: 'latest', polled: true }],
      [],
    )
    expect(yaml).not.toContain('env_vars')
  })

  it('filters blank env keys', () => {
    const yaml = formToYaml(
      [{ name: 'app', registry_image: 'reg/app', image_tag: 'latest', polled: true }],
      [['', 'val'], ['KEY', 'val']],
    )
    expect(yaml).toContain('KEY: val')
    expect(yaml).not.toMatch(/^\s*: val/m)
  })
})

describe('yamlToForm', () => {
  it('parses multi-service YAML', () => {
    const text = `
services:
  - name: app
    registry_image: reg/app
    image_tag: latest
    polled: true
  - name: redis
    registry_image: redis
    image_tag: "7"
    polled: false
env_vars:
  KEY: val
`
    const result = yamlToForm(text)
    expect(result.services).toHaveLength(2)
    expect(result.services[0].name).toBe('app')
    expect(result.services[0].polled).toBe(true)
    expect(result.services[1].name).toBe('redis')
    expect(result.services[1].polled).toBe(false)
    expect(result.envPairs).toEqual([['KEY', 'val']])
  })

  it('parses single-service shorthand', () => {
    const text = `
registry_image: reg/app
image_tag: v2
ports:
  - host_port: 8080
    container_port: 80
`
    const result = yamlToForm(text)
    expect(result.services).toHaveLength(1)
    expect(result.services[0].registry_image).toBe('reg/app')
    expect(result.services[0].image_tag).toBe('v2')
    expect(result.services[0].polled).toBe(true)
    expect(result.services[0].ports).toHaveLength(1)
  })

  it('returns empty arrays for empty/null input', () => {
    expect(yamlToForm('')).toEqual({ services: [], envPairs: [] })
    expect(yamlToForm('null')).toEqual({ services: [], envPairs: [] })
  })

  it('throws on invalid YAML', () => {
    expect(() => yamlToForm('{')).toThrow()
  })
})

describe('round-trip', () => {
  it('preserves data through formToYaml -> yamlToForm', () => {
    const services: ServiceConfig[] = [
      {
        name: 'app',
        registry_image: 'reg/app',
        image_tag: 'latest',
        polled: true,
        ports: [{ host_port: 8080, container_port: 80 }],
        volumes: [{ host_path: './data', container_path: '/app/data', readonly: true }],
        extra_options: 'network_mode: host',
      },
      { name: 'db', registry_image: 'postgres', image_tag: '16', polled: false },
    ]
    const envPairs: [string, string][] = [['DB_URL', 'postgres://localhost']]

    const yamlText = formToYaml(services, envPairs)
    const result = yamlToForm(yamlText)

    expect(result.services).toHaveLength(2)
    expect(result.services[0].name).toBe('app')
    expect(result.services[0].ports).toEqual([{ host_port: 8080, container_port: 80 }])
    expect(result.services[0].volumes).toEqual([{ host_path: './data', container_path: '/app/data', readonly: true }])
    expect(result.services[0].extra_options).toBe('network_mode: host')
    expect(result.services[1].name).toBe('db')
    expect(result.services[1].polled).toBe(false)
    expect(result.envPairs).toEqual([['DB_URL', 'postgres://localhost']])
  })
})
