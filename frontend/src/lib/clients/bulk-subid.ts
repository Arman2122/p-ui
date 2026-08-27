import { RandomUtil } from '@/utils';

/*
bulkClientSubId decides what one generated client's subscription id is.

A subId identifies ONE subscription and the panel refuses a duplicate on every
other path, so a typed value can only be meant for a single client. Stamped
across a batch it let the first email claim it and the backend skipped all the
rest — asking for 50 clients produced 1. Generated per client otherwise, which
is what the untouched field always did. F173.
*/
export function bulkClientSubId(typed: string, count: number): string {
  if (count === 1 && typed) return typed;
  return RandomUtil.randomLowerAndNum(16);
}
