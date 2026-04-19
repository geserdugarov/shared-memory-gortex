'use client'

import { useMemo } from 'react'
import { type ThreeEvent, useThree } from '@react-three/fiber'
import * as THREE from 'three'

export type ThreeNode = {
  pos: THREE.Vector3
  color: THREE.Color
}

// THREE.Color does not parse CSS color-4 (oklch/oklab/color()). Let the
// browser's CSSOM normalise via getComputedStyle — it always returns
// `rgb(...)` / `rgba(...)` for standard (non-wide-gamut) colors, which
// THREE.Color handles. Cached, keyed by raw input.
let _probe: HTMLSpanElement | null = null
const _cssCache = new Map<string, string>()
export function normalizeCssColor(raw: string, fallback = '#9ece6a'): string {
  if (typeof document === 'undefined') return fallback
  const cached = _cssCache.get(raw)
  if (cached) return cached
  if (!_probe) {
    _probe = document.createElement('span')
    _probe.style.display = 'none'
    document.body.appendChild(_probe)
  }
  _probe.style.color = ''
  _probe.style.color = raw
  if (!_probe.style.color) {
    _cssCache.set(raw, fallback)
    return fallback
  }
  const resolved = getComputedStyle(_probe).color || fallback
  // Fall back if the browser returned a wide-gamut color() form THREE can't read.
  const out = /^rgba?\(/.test(resolved) ? resolved : fallback
  _cssCache.set(raw, out)
  return out
}

export function hashStr(s: string): number {
  let h = 2166136261 >>> 0
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619) >>> 0 }
  return h
}

// Sets the raycaster Points threshold once per Canvas. Default (1.0) is too
// coarse at our scene scale (unit ~= 0.1 per node) and causes mis-picks.
export function RaycastThreshold({ threshold }: { threshold: number }) {
  const { raycaster } = useThree()
  raycaster.params.Points = { threshold }
  return null
}

// One draw call for N points. Pass `forceColor` to recolor every point
// (used for "hot" overlays). Clicks report `event.index` which indexes
// back into the original `nodes` array passed by the caller.
export function PointsCloud({
  nodes, size, onClick, forceColor, opacity = 0.95,
}: {
  nodes: ThreeNode[]
  size: number
  onClick?: (e: ThreeEvent<MouseEvent>) => void
  forceColor?: string
  opacity?: number
}) {
  const geom = useMemo(() => {
    const g = new THREE.BufferGeometry()
    if (nodes.length === 0) return g
    const positions = new Float32Array(nodes.length * 3)
    const colors = new Float32Array(nodes.length * 3)
    const override = forceColor ? new THREE.Color(forceColor) : null
    nodes.forEach((n, i) => {
      positions[i * 3] = n.pos.x
      positions[i * 3 + 1] = n.pos.y
      positions[i * 3 + 2] = n.pos.z
      const c = override ?? n.color
      colors[i * 3] = c.r
      colors[i * 3 + 1] = c.g
      colors[i * 3 + 2] = c.b
    })
    g.setAttribute('position', new THREE.BufferAttribute(positions, 3))
    g.setAttribute('color', new THREE.BufferAttribute(colors, 3))
    return g
  }, [nodes, forceColor])

  if (nodes.length === 0) return null
  return (
    <points onClick={onClick}>
      <primitive object={geom} attach="geometry" />
      <pointsMaterial
        vertexColors
        size={size}
        sizeAttenuation
        transparent
        opacity={opacity}
        depthWrite={false}
      />
    </points>
  )
}

export type LineSeg = { a: THREE.Vector3; b: THREE.Vector3; color: THREE.Color }

export function LineSegs({
  segments, opacity = 0.45,
}: {
  segments: LineSeg[]
  opacity?: number
}) {
  const geom = useMemo(() => {
    const g = new THREE.BufferGeometry()
    if (segments.length === 0) return g
    const positions = new Float32Array(segments.length * 6)
    const colors = new Float32Array(segments.length * 6)
    segments.forEach((e, i) => {
      positions[i * 6] = e.a.x
      positions[i * 6 + 1] = e.a.y
      positions[i * 6 + 2] = e.a.z
      positions[i * 6 + 3] = e.b.x
      positions[i * 6 + 4] = e.b.y
      positions[i * 6 + 5] = e.b.z
      colors[i * 6] = e.color.r
      colors[i * 6 + 1] = e.color.g
      colors[i * 6 + 2] = e.color.b
      colors[i * 6 + 3] = e.color.r
      colors[i * 6 + 4] = e.color.g
      colors[i * 6 + 5] = e.color.b
    })
    g.setAttribute('position', new THREE.BufferAttribute(positions, 3))
    g.setAttribute('color', new THREE.BufferAttribute(colors, 3))
    return g
  }, [segments])

  if (segments.length === 0) return null
  return (
    <lineSegments>
      <primitive object={geom} attach="geometry" />
      <lineBasicMaterial vertexColors transparent opacity={opacity} depthWrite={false} />
    </lineSegments>
  )
}

export function EmptyState({ message }: { message: string }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      width: '100%', height: '100%', color: 'var(--fg-3)',
      fontFamily: 'JetBrains Mono, ui-monospace, monospace', fontSize: 12,
    }}>
      {message}
    </div>
  )
}
