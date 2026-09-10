export const APP_NAME = 'Console'
export const APP_VERSION = '1.0.0'

export const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL || '/api/v1'
export const API_TIMEOUT = 30000

export const POLLING_INTERVAL = 3000
export const POLLING_MAX_ATTEMPTS = 100

export const STORAGE_KEYS = {
  AUTH: 'zord_auth',
  CURRENT_ROLE: 'zord_current_role',
  THEME: 'zord_theme',
} as const

export const ROUTES = {
  SIGNIN: '/signin',
  SIGNUP: '/signup',
  OVERVIEW: '/overview',
  ADMIN: '/admin',
} as const

export const ERROR_MESSAGES = {
  NETWORK_ERROR: 'Network error. Please check your connection.',
  UNAUTHORIZED: 'You are not authorized to access this resource.',
  NOT_FOUND: 'Resource not found.',
  VALIDATION_ERROR: 'Please check your input and try again.',
  SERVER_ERROR: 'Server error. Please try again later.',
  UNKNOWN_ERROR: 'An unexpected error occurred.',
} as const
