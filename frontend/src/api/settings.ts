import { fetchURL, fetchJSON } from "./utils";

export function get() {
  return fetchJSON<ISettings>(`/api/settings`, {});
}

export async function update(settings: ISettings) {
  await fetchURL(`/api/settings`, {
    method: "PUT",
    body: JSON.stringify(settings),
  });
}

// alertTest asks the backend to send one test message to the given Baidu IM
// destination, so the admin can confirm the token and group work before saving.
// It throws a StatusError (with the backend's reason) when the send fails.
export async function alertTest(token: string, group: number) {
  await fetchURL(`/api/settings/alert-test`, {
    method: "POST",
    body: JSON.stringify({ loginAlertToken: token, loginAlertGroup: group }),
  });
}
