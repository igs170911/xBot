import { useMemo } from 'react'
import { cx } from '../ui/cx'
import { PAGE_SANDBOX, pageDoc } from './page'

/** Bot 做的網頁，關在沙盒裡顯示（見 page.ts）。網頁自己決定配色，底色先給白的。 */
export function PageFrame({ html, title, className }: { html: string; title: string; className?: string }) {
  const doc = useMemo(() => pageDoc(html), [html])
  return (
    <iframe
      title={title}
      sandbox={PAGE_SANDBOX}
      srcDoc={doc}
      referrerPolicy="no-referrer"
      data-testid="page-frame"
      className={cx('block w-full border-0 bg-white', className)}
    />
  )
}
