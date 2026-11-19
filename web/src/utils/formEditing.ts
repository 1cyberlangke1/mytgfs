
export function markEditable(el: Element | null, editing: boolean) {
  if (!el) return;
  if (el.tagName !== 'INPUT' && el.tagName !== 'TEXTAREA') return;
  (el as HTMLInputElement | HTMLTextAreaElement).readOnly = !editing;
}

export function isTextField(el: HTMLElement | null): boolean {
  if (!el) return false;
  if (el.isContentEditable) return true;
  if (el.tagName === 'TEXTAREA') return true;
  if (el.tagName !== 'INPUT') return false;
  switch ((el as HTMLInputElement).type) {
    case 'search':
    case 'password':
    case 'number':
    case 'email':
    case 'url':
    case 'tel':
    case 'text':
      return true;
    default:
      return false;
  }
}
