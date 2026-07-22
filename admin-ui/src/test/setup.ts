import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

afterEach(() => cleanup())

Object.defineProperty(window.HTMLElement.prototype, 'hasPointerCapture', {
  value: () => false,
})
Object.defineProperty(window.HTMLElement.prototype, 'setPointerCapture', {
  value: () => undefined,
})
Object.defineProperty(window.HTMLElement.prototype, 'releasePointerCapture', {
  value: () => undefined,
})
