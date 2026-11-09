import type { Intent } from './api'

export function ContactNotificationNotice({ notification }: { notification: Intent['contact_notification'] }) {
 if (!notification) return null
 let text = ''
 switch (notification.status) {
  case 'SENT': text = `Emailed the person asking${notification.sent_at ? ` · ${new Date(notification.sent_at).toLocaleTimeString()}` : ''}`; break
  case 'QUEUED': text = 'Email to the person asking is on its way'; break
  case 'SKIPPED_TEST_ADDRESS': text = 'Not emailed (test address)'; break
  case 'FAILED': text = 'Email could not be sent; the person asking can still answer in Pause'; break
  default: return null
 }
 return <p className="helper">{text}</p>
}
