import type { Theme } from 'vitepress'
import DefaultTheme from 'vitepress/theme-without-fonts'
import Layout from './Layout.vue'
import './style.css'

export default {
  extends: DefaultTheme,
  Layout,
  enhanceApp() {
    if (typeof document === 'undefined') return

    document.addEventListener('keydown', (event) => {
      if (event.key !== 'Escape') return

      const mobileToggle = document.querySelector<HTMLButtonElement>(
        '.VPNavBarHamburger[aria-expanded="true"]',
      )
      if (mobileToggle) {
        event.preventDefault()
        mobileToggle.click()
        mobileToggle.focus()
        return
      }

      const active = document.activeElement
      const activeFlyout =
        active instanceof Element ? active.closest<HTMLElement>('.VPNavBarMenuGroup') : null
      const expandedButton = document.querySelector<HTMLButtonElement>(
        '.VPNavBarMenuGroup > .button[aria-expanded="true"]',
      )
      const flyout =
        activeFlyout ??
        expandedButton?.closest<HTMLElement>('.VPNavBarMenuGroup') ??
        document.querySelector<HTMLElement>('.VPNavBarMenuGroup:hover')
      const flyoutToggle = flyout?.querySelector<HTMLButtonElement>(':scope > .button')
      if (!flyout || !flyoutToggle) return

      event.preventDefault()
      if (flyoutToggle.getAttribute('aria-expanded') === 'true') {
        flyoutToggle.click()
      }
      flyout.classList.add('la-flyout-dismissed')

      const restore = () => flyout.classList.remove('la-flyout-dismissed')
      flyout.addEventListener('mouseleave', restore, { once: true })
      flyoutToggle.addEventListener('click', restore, { capture: true, once: true })
      flyoutToggle.focus()
    })
  },
} satisfies Theme
