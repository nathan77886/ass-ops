import { enPart01 } from './en_part01';
import { enPart02 } from './en_part02';
import { enPart03 } from './en_part03';
import { enPart04 } from './en_part04';
import { enPart05 } from './en_part05';
import { enPart06 } from './en_part06';
import { zhPart01 } from './zh_part01';
import { zhPart02 } from './zh_part02';
import { zhPart03 } from './zh_part03';
import { zhPart04 } from './zh_part04';
import { zhPart05 } from './zh_part05';
import { zhPart06 } from './zh_part06';

export type Language = 'en' | 'zh';

export const dictionaries: Record<Language, Record<string, string>> = {
  en: Object.assign({}, enPart01, enPart02, enPart03, enPart04, enPart05, enPart06),
  zh: Object.assign({}, zhPart01, zhPart02, zhPart03, zhPart04, zhPart05, zhPart06)
};
