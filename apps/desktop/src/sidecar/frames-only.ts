/**
 * The sidecar's stdout carries frames only: the desktop treats any other
 * line as a broken protocol and stops the sidecar. What a dependency logs
 * goes to stderr instead, which the desktop shows as the sidecar's own.
 * Imported first, so a module that logs while it loads is covered too.
 */
const toStderr = (...args: unknown[]) => console.error(...args);
console.log = toStderr;
console.info = toStderr;
console.debug = toStderr;
console.dir = toStderr;
console.table = toStderr;
