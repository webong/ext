import type { IO } from './index.mjs';
// Structural Node stream types avoid forcing @types/node on browser consumers.
export function nodeIO(readable: AsyncIterable<Uint8Array> & {
    destroy(): unknown;
 on(event: string, listener: (...args: any[]) => void): unknown;
 once(event: string, listener: (...args: any[]) => void): unknown;
 off(event: string, listener: (...args: any[]) => void): unknown;
}, writable: {
    write(bytes: Uint8Array, callback: (error?: Error | null) => void): unknown;
    destroy(): unknown;
 on(event: string, listener: (...args: any[]) => void): unknown;
 once(event: string, listener: (...args: any[]) => void): unknown;
 off(event: string, listener: (...args: any[]) => void): unknown;
}, options?: {
    close?: () => Promise<void> | void;
}): IO;
