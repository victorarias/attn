/**
 * Extract the repository name from a full "owner/repo" string.
 * Returns the full string if no "/" is found.
 */
export function getRepoName(fullRepo: string): string {
  const parts = fullRepo.split('/');
  return parts[1] || fullRepo;
}
