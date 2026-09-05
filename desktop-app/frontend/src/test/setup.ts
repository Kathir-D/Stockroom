import '@testing-library/jest-dom/vitest'
import {cleanup} from '@testing-library/svelte'
import {afterEach} from 'vitest'

// Unmount anything a test rendered so components can't leak state between tests.
afterEach(() => cleanup())
