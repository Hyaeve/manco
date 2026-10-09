import { defineStore } from 'pinia'
import { api } from '../api'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    user: null,
    ready: false,
  }),
  getters: {
    isAuthenticated: (state) => Boolean(state.user),
  },
  actions: {
    async load() {
      try {
        const payload = await api.me()
        this.user = payload.user
      } catch {
        this.user = null
      } finally {
        this.ready = true
      }
    },
    async login(username, password) {
      const payload = await api.login(username, password)
      this.user = payload.user
    },
    async logout() {
      try {
        await api.logout()
      } finally {
        this.user = null
      }
    },
  },
})
