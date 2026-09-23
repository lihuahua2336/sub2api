import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import LoginView from '@/views/auth/LoginView.vue'

const { getPublicSettingsMock, pushMock, routePath } = vi.hoisted(() => ({
  getPublicSettingsMock: vi.fn(),
  pushMock: vi.fn(),
  routePath: { value: '/login' }
}))

const publicSettings = {
  registration_enabled: true,
  turnstile_enabled: false,
  turnstile_site_key: '',
  tencent_captcha_enabled: false,
  tencent_captcha_app_id: '',
  aliyun_captcha_enabled: false,
  aliyun_captcha_scene_id: '',
  aliyun_captcha_prefix: '',
  linuxdo_oauth_enabled: false,
  dingtalk_oauth_enabled: false,
  wechat_oauth_enabled: false,
  backend_mode_enabled: false,
  oidc_oauth_enabled: false,
  oidc_oauth_provider_name: 'OIDC',
  github_oauth_enabled: false,
  google_oauth_enabled: false,
  password_reset_enabled: false,
  passkey_enabled: false,
  login_agreement_enabled: false,
  login_agreement_documents: []
}

vi.mock('vue-router', () => ({
  useRoute: () => ({ get path() { return routePath.value } }),
  useRouter: () => ({
    push: pushMock,
    currentRoute: { value: { query: {} } }
  })
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({
    global: {
      t: (key: string) => key
    }
  }),
  useI18n: () => ({
    t: (key: string) => key
  })
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({
    login: vi.fn(),
    loginWithPasskey: vi.fn(),
    login2FA: vi.fn()
  }),
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn()
  })
}))

vi.mock('@/api/auth', () => ({
  buildOAuthLoginStartURL: vi.fn(),
  getPublicSettings: (...args: unknown[]) => getPublicSettingsMock(...args),
  isTotp2FARequired: vi.fn(() => false),
  isWeChatWebOAuthEnabled: vi.fn(() => false),
  startOAuthLogin: vi.fn()
}))

function mountLogin() {
  return mount(LoginView, {
    global: {
      stubs: {
        AuthLayout: { template: '<div><slot /><slot name="footer" /></div>' },
        DingTalkOAuthSection: true,
        EmailOAuthButtons: true,
        Icon: true,
        LinuxDoOAuthSection: true,
        LoginAgreementPrompt: true,
        OidcOAuthSection: { template: '<button>oidc-login</button>' },
        RouterLink: { template: '<a><slot /></a>' },
        TotpLoginModal: true,
        TurnstileWidget: true,
        WechatOAuthSection: true,
        transition: false
      }
    }
  })
}

describe('LoginView registration entry', () => {
  beforeEach(() => {
    getPublicSettingsMock.mockReset()
    pushMock.mockReset()
    routePath.value = '/login'
    vi.stubEnv('VITE_OIDC_ONLY', '')
    getPublicSettingsMock.mockResolvedValue(publicSettings)
  })

  it('shows the registration entry when registration is enabled', async () => {
    const wrapper = mountLogin()
    await flushPromises()

    expect(wrapper.text()).toContain('auth.signUp')
  })

  it('hides the registration entry when registration is disabled', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      registration_enabled: false
    })

    const wrapper = mountLogin()
    await flushPromises()

    expect(wrapper.text()).not.toContain('auth.signUp')
  })

  it('shows only OIDC login and hides registration in OIDC-only mode', async () => {
    vi.stubEnv('VITE_OIDC_ONLY', 'true')
    getPublicSettingsMock.mockResolvedValue({
      ...publicSettings,
      registration_enabled: true,
      oidc_oauth_enabled: true,
      github_oauth_enabled: true,
      google_oauth_enabled: true,
      linuxdo_oauth_enabled: true,
      dingtalk_oauth_enabled: true,
      wechat_oauth_enabled: true,
      passkey_enabled: true
    })

    const wrapper = mountLogin()
    await flushPromises()

    expect(wrapper.find('#password').exists()).toBe(false)
    expect(wrapper.text()).toContain('oidc-login')
    expect(wrapper.text()).not.toContain('auth.signUp')
    expect(wrapper.findComponent({ name: 'EmailOAuthButtons' }).exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'LinuxDoOAuthSection' }).exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'DingTalkOAuthSection' }).exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'WechatOAuthSection' }).exists()).toBe(false)
  })

  it('keeps the password form on the dedicated admin login path', async () => {
    vi.stubEnv('VITE_OIDC_ONLY', 'true')
    routePath.value = '/admin/login'
    getPublicSettingsMock.mockResolvedValue({
      ...publicSettings,
      oidc_oauth_enabled: true
    })

    const wrapper = mountLogin()
    await flushPromises()

    expect(wrapper.find('#password').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('oidc-login')
  })
})
