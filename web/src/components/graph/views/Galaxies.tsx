'use client'

import { useMemo } from 'react'
import { Canvas, type ThreeEvent } from '@react-three/fiber'
import { OrbitControls, Html } from '@react-three/drei'
import * as THREE from 'three'
import { useInspector } from '@/lib/inspector'
import type { GraphData, GortexNode } from '@/lib/types'
import type { Repo } from '@/lib/schema'
import { computeDegree, groupByRepo, stableSortByDegreeDesc, seededRng } from './layout'
import {
  EmptyState, LineSegs, PointsCloud, RaycastThreshold,
  hashStr, normalizeCssColor, type LineSeg,
} from './three-common'

const MAX_PER_REPO = 80
const MAX_EDGES = 500
const SCENE_RADIUS = 6
const GALAXY_RADIUS = 2.2
const POINT_SIZE = 0.085
const HOT_POINT_SIZE = 0.14
const HOT_COLOR = '#f7768e'
const ACCENT_COLOR = '#9ece6a'
const PICK_THRESHOLD = 0.08

type GNode = {
  pos: THREE.Vector3
  color: THREE.Color
  repo: string
  hot: boolean
  id: string
  node: GortexNode
}

type GalaxyCenter = { repo: Repo; center: THREE.Vector3 }

export function ThreeDGalaxies({
  graph, repos, filterRepos,
}: {
  graph: GraphData | null
  repos: Repo[]
  filterRepos: Set<string>
}) {
  const setSym = useInspector((s) => s.setSym)

  const { coolNodes, hotNodes, edges, galaxies } = useMemo(() => {
    const empty = {
      coolNodes: [] as GNode[],
      hotNodes: [] as GNode[],
      edges: [] as LineSeg[],
      galaxies: [] as GalaxyCenter[],
    }
    if (!graph) return empty
    const degree = computeDegree(graph.nodes, graph.edges)
    const buckets = groupByRepo(graph.nodes)
    const visibleRepos = repos
      .filter((r) => !filterRepos.size || filterRepos.has(r.id))
      .filter((r) => (buckets.get(r.id)?.length ?? 0) > 0)

    const galaxies: GalaxyCenter[] = visibleRepos.map((rep, idx) => {
      const a = (idx / Math.max(1, visibleRepos.length)) * Math.PI * 2
      const yOff = ((hashStr(rep.id) % 1000) / 1000 - 0.5) * 2.5
      return {
        repo: rep,
        center: new THREE.Vector3(
          Math.cos(a) * SCENE_RADIUS,
          yOff,
          Math.sin(a) * SCENE_RADIUS,
        ),
      }
    })

    const coolNodes: GNode[] = []
    const hotNodes: GNode[] = []
    const index = new Map<string, GNode>()
    galaxies.forEach(({ repo, center }) => {
      const rng = seededRng(hashStr(repo.id) + 59)
      const bucket = buckets.get(repo.id) ?? []
      const sorted = stableSortByDegreeDesc(bucket, degree).slice(0, MAX_PER_REPO)
      const maxDeg = Math.max(1, ...sorted.map((n) => degree.get(n.id) ?? 0))
      const col = new THREE.Color(normalizeCssColor(repo.color))
      for (const n of sorted) {
        const rr = Math.cbrt(rng()) * GALAXY_RADIUS
        const theta = rng() * Math.PI * 2
        const phi = Math.acos(2 * rng() - 1)
        const deg = degree.get(n.id) ?? 0
        const g: GNode = {
          pos: new THREE.Vector3(
            center.x + rr * Math.sin(phi) * Math.cos(theta),
            center.y + rr * Math.sin(phi) * Math.sin(theta),
            center.z + rr * Math.cos(phi),
          ),
          color: col.clone(),
          repo: repo.id,
          hot: deg >= Math.max(8, maxDeg * 0.7),
          id: n.id,
          node: n,
        }
        ;(g.hot ? hotNodes : coolNodes).push(g)
        index.set(n.id, g)
      }
    })

    const pink = new THREE.Color(HOT_COLOR)
    const accent = new THREE.Color(ACCENT_COLOR)
    const edges: LineSeg[] = []
    for (const e of graph.edges) {
      const a = index.get(e.from)
      const b = index.get(e.to)
      if (!a || !b || a === b) continue
      const crossHot = !!e.cross_repo && e.kind === 'calls'
      const same = a.repo === b.repo
      const color = crossHot ? pink : same ? a.color : accent
      edges.push({ a: a.pos, b: b.pos, color })
      if (edges.length >= MAX_EDGES) break
    }
    return { coolNodes, hotNodes, edges, galaxies }
  }, [graph, repos, filterRepos])

  const pick = (list: GNode[]) => (e: ThreeEvent<MouseEvent>) => {
    e.stopPropagation()
    const idx = e.index
    if (idx === undefined) return
    const n = list[idx]
    if (!n) return
    setSym({
      id: n.node.id,
      kind: (n.node.kind as 'function') ?? 'function',
      name: n.node.name,
      repo: n.repo,
      file: n.node.file_path,
      sig: '', callers: 0, callees: 0, community: '', caveats: [],
    })
  }

  if (!graph || (coolNodes.length === 0 && hotNodes.length === 0)) {
    return <EmptyState message="No graph data — run `gortex index .` to populate." />
  }

  return (
    <Canvas
      camera={{ position: [0, 4.5, 14], fov: 55 }}
      dpr={[1, 2]}
      style={{ width: '100%', height: '100%' }}
    >
      <RaycastThreshold threshold={PICK_THRESHOLD} />
      <ambientLight intensity={0.6} />

      {galaxies.map((g) => (
        <mesh key={g.repo.id} position={g.center}>
          <sphereGeometry args={[GALAXY_RADIUS * 1.1, 24, 24]} />
          <meshBasicMaterial
            color={normalizeCssColor(g.repo.color)} transparent opacity={0.045}
            depthWrite={false}
          />
        </mesh>
      ))}

      <LineSegs segments={edges} opacity={0.45} />
      <PointsCloud nodes={coolNodes} size={POINT_SIZE} onClick={pick(coolNodes)} />
      <PointsCloud nodes={hotNodes}  size={HOT_POINT_SIZE} onClick={pick(hotNodes)} forceColor={HOT_COLOR} />

      {galaxies.map((g) => (
        <Html
          key={g.repo.id}
          position={[g.center.x, g.center.y + GALAXY_RADIUS + 0.35, g.center.z]}
          center
          occlude={false}
          style={{ pointerEvents: 'none' }}
        >
          <div style={{
            display: 'inline-flex', alignItems: 'center', gap: 6,
            background: 'var(--bg-1)', color: 'var(--fg-1)',
            border: `1px solid ${g.repo.color}80`, borderRadius: 9,
            padding: '2px 8px',
            font: '10px JetBrains Mono, ui-monospace, monospace',
            whiteSpace: 'nowrap', opacity: 0.9,
          }}>
            <span style={{
              width: 6, height: 6, background: g.repo.color, borderRadius: '50%',
            }} />
            {g.repo.id}
          </div>
        </Html>
      ))}

      <OrbitControls enablePan enableZoom enableRotate makeDefault />
    </Canvas>
  )
}
