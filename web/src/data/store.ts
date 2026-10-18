import { useSyncExternalStore } from 'react'

// 很小的全域狀態：get／set／subscribe，給 useSyncExternalStore 用（不另外裝狀態管理套件）。
export type Store<T> = {
  get: () => T
  set: (next: T | ((prev: T) => T)) => void
  subscribe: (fn: () => void) => () => void
}

export function createStore<T>(initial: T): Store<T> {
  let state = initial
  const listeners = new Set<() => void>()
  return {
    get: () => state,
    set: (next) => {
      const value = typeof next === 'function' ? (next as (prev: T) => T)(state) : next
      if (Object.is(value, state)) return
      state = value
      listeners.forEach((fn) => fn())
    },
    subscribe: (fn) => {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
  }
}

export function useStore<T>(store: Store<T>): T {
  return useSyncExternalStore(store.subscribe, store.get, store.get)
}
