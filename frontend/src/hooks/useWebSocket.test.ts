import { describe, it, expect } from 'vitest'
import type { DeployEvent } from './useWebSocket'

describe('DeployEvent type', () => {
  it('parses a valid event', () => {
    const raw = '{"type":"step_start","project_id":"app1","timestamp":"2026-03-09T12:00:00Z","step":"pull"}'
    const event: DeployEvent = JSON.parse(raw)
    expect(event.type).toBe('step_start')
    expect(event.project_id).toBe('app1')
    expect(event.step).toBe('pull')
  })

  it('handles optional fields', () => {
    const raw = '{"type":"log","project_id":"app1","timestamp":"2026-03-09T12:00:00Z","message":"pulling...","error":true}'
    const event: DeployEvent = JSON.parse(raw)
    expect(event.type).toBe('log')
    expect(event.message).toBe('pulling...')
    expect(event.error).toBe(true)
    expect(event.step).toBeUndefined()
  })
})
