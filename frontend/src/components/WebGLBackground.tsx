import { useEffect, useRef } from 'react'

const VERTEX_SHADER = `
attribute vec2 a_position;
varying vec2 v_texCoord;
void main() {
  v_texCoord = a_position * 0.5 + 0.5;
  gl_Position = vec4(a_position, 0.0, 1.0);
}`

const FRAGMENT_SHADER = `
precision highp float;

varying vec2 v_texCoord;
uniform float u_time;
uniform vec2 u_resolution;
uniform vec2 u_mouse;

vec3 colorBg      = vec3(0.039, 0.043, 0.055);
vec3 colorEmerald = vec3(0.063, 0.725, 0.506);
vec3 colorIndigo  = vec3(0.15, 0.1, 0.3);

float hash(vec2 p) {
    p = fract(p * vec2(123.34, 456.21));
    p += dot(p, p + 45.32);
    return fract(p.x * p.y);
}

float noise(vec2 p) {
    vec2 i = floor(p);
    vec2 f = fract(p);
    f = f * f * (3.0 - 2.0 * f);
    float a = hash(i);
    float b = hash(i + vec2(1.0, 0.0));
    float c = hash(i + vec2(0.0, 1.0));
    float d = hash(i + vec2(1.0, 1.0));
    return mix(mix(a, b, f.x), mix(c, d, f.x), f.y);
}

void main() {
    vec2 uv = v_texCoord;
    vec2 aspectUv = (uv - 0.5) * vec2(u_resolution.x / u_resolution.y, 1.0) + 0.5;

    // Glow blobs
    float glowTL = smoothstep(1.0, 0.0, length(uv - vec2(0.1, 0.9)) * 1.5);
    float glowBR = smoothstep(1.0, 0.0, length(uv - vec2(0.9, 0.1)) * 1.2);
    vec3 finalColor = colorBg;
    finalColor += colorEmerald * glowTL * (0.08 + 0.02 * sin(u_time * 0.5));
    finalColor += colorIndigo  * glowBR * (0.10 + 0.03 * cos(u_time * 0.7));

    // Grid wave
    vec2 gridUv = aspectUv * 40.0;
    gridUv.y += sin(gridUv.x * 0.2 + u_time * 0.5) * 0.5;
    gridUv.x += cos(gridUv.y * 0.2 + u_time * 0.5) * 0.5;
    vec2 gridLocal = fract(gridUv) - 0.5;
    float gridDots = smoothstep(0.15, 0.05, length(gridLocal));
    finalColor = mix(finalColor, colorEmerald, gridDots * 0.15);

    // Particle network
    vec2 particleUv = aspectUv * 15.0;
    vec2 pId    = floor(particleUv);
    vec2 pLocal = fract(particleUv) - 0.5;

    for (int y = -1; y <= 1; y++) {
        for (int x = -1; x <= 1; x++) {
            vec2 neighbor = vec2(float(x), float(y));
            vec2 point = neighbor + (vec2(hash(pId + neighbor), hash(pId + neighbor + 12.0)) - 0.5) * 0.8;
            point += 0.4 * vec2(
                sin(u_time * 0.3 + hash(pId + neighbor) * 10.0),
                cos(u_time * 0.3 + hash(pId + neighbor + 5.0) * 10.0)
            );
            float dist = length(pLocal - point);
            float pSize = 0.02 + 0.03 * hash(pId + neighbor + 20.0);
            float spark = smoothstep(pSize, 0.0, dist);
            finalColor += colorEmerald * spark * (0.5 + 0.5 * sin(u_time + hash(pId + neighbor) * 6.28));
            if (dist < 0.3) {
                finalColor += colorEmerald * (1.0 - dist / 0.3) * 0.05;
            }
        }
    }

    // Scan line
    float scanLine = fract(uv.y - u_time * 0.1);
    float scanGlow = smoothstep(0.01, 0.0, abs(scanLine - 0.5)) * 0.03;
    finalColor += colorEmerald * scanGlow;

    gl_FragColor = vec4(finalColor, 1.0);
}`

export default function WebGLBackground() {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const gl = canvas.getContext('webgl') || canvas.getContext('experimental-webgl') as WebGLRenderingContext | null
    if (!gl) return

    // Sync canvas size
    function syncSize() {
      if (!canvas) return
      const w = canvas.clientWidth || window.innerWidth
      const h = canvas.clientHeight || window.innerHeight
      if (canvas.width !== w || canvas.height !== h) {
        canvas.width = w
        canvas.height = h
      }
    }

    const ro = new ResizeObserver(syncSize)
    ro.observe(canvas)
    syncSize()

    function compileShader(type: number, src: string) {
      const shader = gl!.createShader(type)!
      gl!.shaderSource(shader, src)
      gl!.compileShader(shader)
      return shader
    }

    const prog = gl.createProgram()!
    gl.attachShader(prog, compileShader(gl.VERTEX_SHADER, VERTEX_SHADER))
    gl.attachShader(prog, compileShader(gl.FRAGMENT_SHADER, FRAGMENT_SHADER))
    gl.linkProgram(prog)
    gl.useProgram(prog)

    const buf = gl.createBuffer()
    gl.bindBuffer(gl.ARRAY_BUFFER, buf)
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), gl.STATIC_DRAW)

    const pos = gl.getAttribLocation(prog, 'a_position')
    gl.enableVertexAttribArray(pos)
    gl.vertexAttribPointer(pos, 2, gl.FLOAT, false, 0, 0)

    const uTime  = gl.getUniformLocation(prog, 'u_time')
    const uRes   = gl.getUniformLocation(prog, 'u_resolution')
    const uMouse = gl.getUniformLocation(prog, 'u_mouse')

    const mouse = { x: canvas.width / 2, y: canvas.height / 2 }
    const onMouseMove = (e: MouseEvent) => {
      const rect = canvas.getBoundingClientRect()
      if (rect.width && rect.height) {
        mouse.x = ((e.clientX - rect.left) / rect.width) * canvas.width
        mouse.y = (1 - (e.clientY - rect.top) / rect.height) * canvas.height
      }
    }
    window.addEventListener('mousemove', onMouseMove)

    let animId: number
    function render(t: number) {
      syncSize()
      gl!.viewport(0, 0, canvas!.width, canvas!.height)
      gl!.uniform1f(uTime, t * 0.001)
      gl!.uniform2f(uRes, canvas!.width, canvas!.height)
      gl!.uniform2f(uMouse, mouse.x, mouse.y)
      gl!.drawArrays(gl!.TRIANGLE_STRIP, 0, 4)
      animId = requestAnimationFrame(render)
    }
    animId = requestAnimationFrame(render)

    return () => {
      cancelAnimationFrame(animId)
      ro.disconnect()
      window.removeEventListener('mousemove', onMouseMove)
    }
  }, [])

  return (
    <canvas
      ref={canvasRef}
      className="absolute inset-0 w-full h-full"
      style={{ display: 'block' }}
    />
  )
}
