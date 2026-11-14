// Original Russian summaries; source scope and review notes: docs/exercise-sources.md.
export const MUSCLE_GROUPS = Object.freeze({ legs: 'Ноги', glutes: 'Ягодицы', chest: 'Грудь', back: 'Спина', core: 'Корпус', arms: 'Руки' });
export const EQUIPMENT = Object.freeze({ none: 'Без инвентаря', mat: 'Коврик', wall: 'Стена', dumbbells: 'Гантели' });

const aceLowerBody = {
  title: 'Ankles, Knees and Hips: 10 Joint-Friendly Exercises',
  publisher: 'American Council on Exercise (ACE)',
  url: 'https://www.acefitness.org/resources/pros/expert-articles/9158/ankles-knees-and-hips-10-joint-friendly-exercises-for-the-lower-extremity/',
};
const nhsStrength = { title: 'Strength exercises', publisher: 'NHS', url: 'https://www.nhs.uk/live-well/exercise/strength-exercises/' };
const nhsRunners = {
  title: 'Injury prevention advice for runners — Dead bug / Forward plank',
  publisher: 'Newcastle Hospitals NHS Foundation Trust',
  url: 'https://www.newcastle-hospitals.nhs.uk/services/injury-prevention-advice-for-runners/',
};

const exercises = [
  {
    id: 'squat', name: 'Приседание', aliases: ['присед', 'приседания', 'squat', 'bodyweight squat'],
    muscles: ['legs', 'glutes'], equipment: ['none'],
    summary: 'Сгибание ног и таза с опорой на всю стопу.',
    steps: [
      'Встаньте, поставив стопы немного шире таза; носки слегка разверните наружу.',
      'На вдохе отведите таз назад и вниз, сгибая колени в направлении носков.',
      'Остановитесь до отрыва пяток или потери устойчивого положения спины.',
      'На выдохе оттолкнитесь стопами от пола и поднимите таз и корпус вместе.',
    ],
    cues: ['Сохраняйте опору на пятку и переднюю часть стопы.', 'Колени следуют направлению носков.'],
    mistakes: ['Отрыв пяток.', 'Завал коленей внутрь и округление спины.'],
    easier: 'Уменьшите глубину приседания, сохраняя устойчивость стоп и корпуса.',
    sources: [{ title: 'Bodyweight Squat', publisher: 'American Council on Exercise (ACE)', url: 'https://www.acefitness.org/resources/everyone/exercise-library/135/bodyweight-squat/' }],
  },
  {
    id: 'reverse-lunge', name: 'Выпад назад', aliases: ['обратный выпад', 'выпады', 'reverse lunge'],
    muscles: ['legs', 'glutes'], equipment: ['none'],
    summary: 'Шаг назад со сгибанием обеих ног.',
    steps: [
      'Встаньте прямо, стопы на ширине таза.',
      'Шагните одной ногой назад и опустите таз, сгибая оба колена; заднее колено направьте к полу.',
      'Оттолкнитесь передней стопой и вернитесь в стойку. Затем смените ногу.',
    ],
    cues: ['Держите корпус устойчиво.', 'Переднее колено направляйте по линии стопы.'],
    mistakes: ['Завал переднего колена внутрь.', 'Падение на заднее колено.'],
    easier: 'Начните с неглубокого выпада, удерживаясь за спинку устойчивого стула.',
    sources: [aceLowerBody, { title: 'Chair Based Exercises for Strength and Balance — Reverse lunges', publisher: 'Oxford University Hospitals NHS Foundation Trust', url: 'https://www.ouh.nhs.uk/media/i1jharcr/85296exercises.pdf' }],
  },
  {
    id: 'glute-bridge', name: 'Ягодичный мост', aliases: ['мостик', 'подъём таза', 'glute bridge'],
    muscles: ['glutes', 'core'], equipment: ['none', 'mat'],
    summary: 'Подъём таза лёжа с согнутыми ногами. Коврик необязателен.',
    steps: [
      'Лягте на спину, согните колени, поставьте стопы на ширине таза; руки оставьте на полу.',
      'Слегка напрягите живот. На выдохе поднимите таз, сокращая ягодицы и сохраняя опору на стопы.',
      'Остановитесь до прогиба в пояснице. На вдохе плавно опустите таз на пол.',
    ],
    cues: ['Поднимайте таз за счёт ягодиц.', 'Сохраняйте лёгкое напряжение живота.'],
    mistakes: ['Подъём слишком высоко с прогибом в пояснице.', 'Резкое опускание таза.'],
    easier: 'Поднимайте таз ниже, пока можете контролировать положение поясницы.',
    sources: [{ title: 'Glute Bridge', publisher: 'American Council on Exercise (ACE)', url: 'https://www.acefitness.org/resources/everyone/exercise-library/49/glute-bridge/' }],
  },
  {
    id: 'push-up', name: 'Отжимание от пола', aliases: ['отжимания', 'push up', 'push-up'],
    muscles: ['chest', 'arms', 'core'], equipment: ['none', 'mat'],
    summary: 'Сгибание рук в упоре с устойчивым корпусом. Коврик необязателен.',
    steps: [
      'Из положения на четвереньках поставьте ладони под плечи и выпрямите ноги, опираясь на носки.',
      'Напрягите живот и ягодицы. Плавно согните локти, опуская грудь и таз вместе.',
      'Оттолкнитесь ладонями и выпрямите руки, сохраняя линию головы, корпуса и ног.',
    ],
    cues: ['Двигайте корпус как единое целое.', 'Держите голову на одной линии со спиной.'],
    mistakes: ['Провисание поясницы.', 'Подъём таза раньше груди.'],
    easier: 'Перейдите к отжиманию от стены, сохраняя ровное положение корпуса.',
    sources: [{ title: 'Push-up', publisher: 'American Council on Exercise (ACE)', url: 'https://www.acefitness.org/resources/everyone/exercise-library/41/push-up/' }, nhsStrength],
  },
  {
    id: 'wall-push-up', name: 'Отжимание от стены', aliases: ['отжимания у стены', 'отжимания от стены', 'wall push up', 'wall press-up'],
    muscles: ['chest', 'arms'], equipment: ['wall'],
    summary: 'Отжимание стоя с опорой ладонями на стену.',
    steps: [
      'Встаньте примерно на расстоянии вытянутой руки от стены. Поставьте ладони на уровне груди, пальцами вверх.',
      'Медленно согните локти, приближая корпус к стене; локти держите ближе к бокам.',
      'Плавно выпрямите руки и вернитесь в исходное положение.',
    ],
    cues: ['Сохраняйте спину ровной.', 'Приближайтесь к стене всем корпусом.'],
    mistakes: ['Прогиб в пояснице.', 'Рывок при отталкивании.'],
    easier: 'Подойдите немного ближе к стене и уменьшите сгибание рук.',
    sources: [nhsStrength],
  },
  {
    id: 'plank', name: 'Планка на предплечьях', aliases: ['планка', 'plank', 'forearm plank'],
    muscles: ['core'], equipment: ['none', 'mat'],
    summary: 'Удержание корпуса с опорой на предплечья и носки. Коврик необязателен.',
    steps: [
      'Лягте на живот, поставьте предплечья на пол и подогните пальцы ног.',
      'Поднимите корпус, сохраняя ровную линию тела. Удерживайте положение и продолжайте дышать.',
      'Завершите удержание, мягко опустив колени и корпус на пол.',
    ],
    cues: ['Удерживайте таз на линии корпуса.', 'Дышите без задержки.'],
    mistakes: ['Провисание поясницы.', 'Задержка дыхания.'],
    easier: 'Сократите время удержания, завершая его до потери ровного положения.',
    sources: [nhsRunners, { title: 'Combined press ups — Plank', publisher: 'South Tees Hospitals NHS Foundation Trust', url: 'https://www.southtees.nhs.uk/resources/combined-press-ups/' }],
  },
  {
    id: 'bird-dog', name: 'Рука и нога на четвереньках', aliases: ['птица-собака', 'bird dog', 'bird-dog'],
    muscles: ['core', 'back', 'glutes'], equipment: ['none', 'mat'],
    summary: 'Вытяжение противоположных руки и ноги при неподвижном корпусе. Коврик необязателен.',
    steps: [
      'Встаньте на четвереньки: ладони под плечами, колени под тазом. Слегка напрягите живот.',
      'Медленно вытяните одну руку вперёд и противоположную ногу назад примерно до линии корпуса.',
      'Верните ладонь и колено на пол без поворота таза. Затем смените сторону.',
    ],
    cues: ['Сохраняйте плечи и таз параллельно полу.', 'Поднимайте конечности лишь пока поясница остаётся неподвижной.'],
    mistakes: ['Слишком высокий подъём ноги с прогибом.', 'Разворот таза вслед за ногой.'],
    easier: 'Уменьшите высоту и длину вытяжения, сохраняя устойчивость корпуса.',
    sources: [{ title: 'Bird-dog', publisher: 'American Council on Exercise (ACE)', url: 'https://www.acefitness.org/resources/everyone/exercise-library/14/bird-dog/' }],
  },
  {
    id: 'dead-bug', name: 'Мёртвый жук', aliases: ['жук', 'dead bug', 'dead-bug'],
    muscles: ['core'], equipment: ['none', 'mat'],
    summary: 'Движение противоположных руки и ноги лёжа на спине. Коврик необязателен.',
    steps: [
      'Лягте на спину. Поднимите руки к потолку, согните тазобедренные и коленные суставы примерно под прямым углом.',
      'Сохраняя поясницу у пола, медленно опустите одну руку за голову и вытяните противоположную ногу к полу.',
      'Верните руку и ногу в исходное положение. Затем смените сторону.',
    ],
    cues: ['Оставляйте корпус неподвижным.', 'Двигайте противоположные руку и ногу.'],
    mistakes: ['Прогиб поясницы при вытяжении.', 'Резкое опускание конечностей.'],
    easier: 'Уменьшите амплитуду: не опускайте руку и ногу до положения, в котором поясница отрывается.',
    sources: [nhsRunners],
  },
  {
    id: 'calf-raise', name: 'Подъём на носки', aliases: ['подъём пяток', 'подъёмы на носки', 'икры', 'calf raise'],
    muscles: ['legs'], equipment: ['wall'],
    summary: 'Подъём пяток стоя с опорой руками на стену.',
    steps: [
      'Встаньте близко к стене, стопы на ширине таза, носки вперёд. Обопритесь ладонями на стену.',
      'На выдохе плавно поднимите обе пятки, сохраняя ноги выпрямленными и стопы без разворота.',
      'После короткой остановки на вдохе плавно опустите пятки на пол.',
    ],
    cues: ['Используйте стену для равновесия.', 'Поднимайтесь плавно, без подскока.'],
    mistakes: ['Разворот стоп во время подъёма.', 'Падение пяток на пол.'],
    easier: 'Уменьшите высоту подъёма, сохраняя опору руками на стену.',
    sources: [{ title: 'Standing Calf Raises — Wall', publisher: 'American Council on Exercise (ACE)', url: 'https://www.acefitness.org/resources/everyone/exercise-library/73/standing-calf-raises-wall/' }],
  },
  {
    id: 'dumbbell-row', name: 'Тяга гантели в наклоне', aliases: ['тяга к поясу', 'dumbbell row', 'bent-over row'],
    muscles: ['back', 'arms'], equipment: ['dumbbells'],
    summary: 'Тяга одной рукой стоя в наклоне; корпус остаётся устойчивым.',
    steps: [
      'Возьмите гантель, поставьте стопы примерно на ширине плеч. Слегка согните колени и наклонитесь от таза с ровной спиной.',
      'Из положения руки под плечом плавно поднимите гантель, ведя локоть вдоль корпуса до уровня спины.',
      'Медленно опустите гантель. После завершения движения смените руку.',
    ],
    cues: ['Сохраняйте положение спины и лёгкое напряжение живота.', 'Не заворачивайте плечо вперёд.'],
    mistakes: ['Рывок и поворот корпуса.', 'Округление спины.'],
    easier: 'Возьмите более лёгкую гантель, с которой можете сохранить неподвижный корпус.',
    sources: [{ title: 'Video: Bent-over row with dumbbell', publisher: 'Mayo Clinic', url: 'https://www.mayoclinic.org/healthy-lifestyle/fitness/multimedia/bent-over-row/vid-20084680' }],
  },
  {
    id: 'biceps-curl', name: 'Сгибание рук с гантелями', aliases: ['бицепс', 'подъём на бицепс', 'biceps curl'],
    muscles: ['arms'], equipment: ['dumbbells'],
    summary: 'Сгибание локтей с неподвижными плечами и прямыми запястьями.',
    steps: [
      'Встаньте устойчиво, опустите руки с гантелями вдоль тела; разверните ладони вперёд.',
      'Плавно согните локти и поднимите гантели, удерживая локти возле корпуса.',
      'Медленно опустите гантели в исходное положение.',
    ],
    cues: ['Сохраняйте запястья прямыми.', 'Двигайте предплечья без раскачивания плеч.'],
    mistakes: ['Раскачивание корпуса и локтей.', 'Сгибание запястий под весом.'],
    easier: 'Используйте более лёгкие гантели, сохраняя плавное движение.',
    sources: [{ title: 'Video: Biceps curl with dumbbell', publisher: 'Mayo Clinic', url: 'https://www.mayoclinic.org/healthy-lifestyle/fitness/multimedia/biceps-curl/vid-20084675' }],
  },
  {
    id: 'hip-hinge', name: 'Наклон от таза', aliases: ['наклон с отведением таза', 'хип-хиндж', 'hip hinge'],
    muscles: ['glutes', 'legs'], equipment: ['none'],
    summary: 'Наклон корпуса через движение таза назад.',
    steps: [
      'Встаньте, стопы на ширине таза, колени слегка согнуты.',
      'Отведите таз назад, наклоняя корпус с нейтральным положением спины.',
      'Сократите ягодицы и вернитесь в вертикальное положение.',
    ],
    cues: ['Направляйте таз назад.', 'Сохраняйте небольшое сгибание коленей.'],
    mistakes: ['Округление поясницы.', 'Замена наклона глубоким приседанием.'],
    easier: 'Начните с небольшого наклона, сохраняя контроль спины.',
    sources: [aceLowerBody],
  },
];

// Consumers share this catalogue; freeze nested arrays and sources as well as records.
for (const exercise of exercises) {
  for (const value of Object.values(exercise)) {
    if (!Array.isArray(value)) continue;
    for (const item of value) if (item && typeof item === 'object') Object.freeze(item);
    Object.freeze(value);
  }
  Object.freeze(exercise);
}
export const EXERCISES = Object.freeze(exercises);
