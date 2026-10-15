import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderToString } from 'react-dom/server'
import EditProfileModal from './EditProfileModal'
import { useAuthStore } from '../../store/authStore'

describe('EditProfileModal Component', () => {
  const onClose = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
    useAuthStore.setState({
      user: {
        userId: 'usr_test_123',
        email: 'tester@tradedrift.local',
        username: 'Existing User',
      },
      accessToken: 'token',
      refreshToken: 'refresh',
    })
  })

  it('renders nothing when isOpen is false', () => {
    const html = renderToString(<EditProfileModal isOpen={false} onClose={onClose} />)
    expect(html).toBe('')
  })

  it('renders dialog structure and accessibility attributes when isOpen is true', () => {
    const html = renderToString(<EditProfileModal isOpen={true} onClose={onClose} />)
    expect(html).toContain('role="dialog"')
    expect(html).toContain('aria-modal="true"')
    expect(html).toContain('id="profile-edit-modal-title"')
    expect(html).toContain('Edit Profile')
    expect(html).toContain('Display Name')
    expect(html).toContain('Email Address')
    expect(html).toContain('value="Existing User"')
    expect(html).toContain('value="tester@tradedrift.local"')
    expect(html).toContain('Save Changes')
    expect(html).toContain('Cancel')
  })

  it('renders local session notice explaining browser session synchronization', () => {
    const html = renderToString(<EditProfileModal isOpen={true} onClose={onClose} />)
    expect(html).toContain('Local session notice:')
    expect(html).toContain('synchronized with your active browser session')
  })
})
