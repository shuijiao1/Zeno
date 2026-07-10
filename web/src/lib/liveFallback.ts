export function shouldStartHttpFallback(fallbackStarted: boolean, receivedLiveFrame: boolean): boolean {
  return !fallbackStarted && !receivedLiveFrame
}
