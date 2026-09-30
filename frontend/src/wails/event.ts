import { Events } from "@wailsio/runtime";

export type Event<T> = { payload: T };
export type UnlistenFn = () => void;

export function listen<T>(
  eventName: string,
  handler: (event: Event<T>) => void,
): Promise<UnlistenFn> {
  const unlisten = Events.On(eventName, (event) => {
    handler({ payload: event.data as T });
  });
  return Promise.resolve(unlisten);
}
